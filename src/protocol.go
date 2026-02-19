package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

var ErrBlobTooLarge = errors.New("blob too large")

const nonceSize = 12

const (
	MsgUpload        = 'U'
	MsgDownload      = 'D'
	MsgSecureUpload  = 'S'
)

const (
	StatusOK            = 0
	StatusChecksumError = 1
	StatusError         = 2
	StatusNotFound      = 3
)

const CodeLength = 6

const FileChunkSize = 256 * 1024

type EncryptedChunk struct {
	Nonce  [12]byte
	Sealed []byte
}

type ProgressFunc func(sent, total int64)

const sendChunkSize = 256 * 1024

func SendFile(w io.Writer, name string, content io.Reader, size int64, progress ProgressFunc) ([]byte, error) {
	nameBytes := []byte(name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}

	hasher := sha256.New()
	tee := io.TeeReader(content, hasher)
	body, err := io.ReadAll(tee)
	if err != nil {
		return nil, err
	}
	checksum := hasher.Sum(nil)

	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return nil, err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return nil, err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(len(body))); err != nil {
		return nil, err
	}
	if _, err := w.Write(checksum); err != nil {
		return nil, err
	}
	total := int64(len(body))
	var sent int64
	for sent < total {
		n := sendChunkSize
		if total-sent < int64(n) {
			n = int(total - sent)
		}
		_, err := w.Write(body[sent : sent+int64(n)])
		if err != nil {
			return nil, err
		}
		sent += int64(n)
		if progress != nil {
			progress(sent, total)
		}
	}

	return checksum, nil
}

func ReadFileHeader(r io.Reader) (name string, size uint64, expectedChecksum []byte, err error) {
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return
	}
	name = string(nameBuf)

	if err = binary.Read(r, binary.BigEndian, &size); err != nil {
		return
	}
	expectedChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, expectedChecksum); err != nil {
		return
	}
	return
}

func ReadAndVerifyBody(r io.Reader, w io.Writer, size uint64) (actualChecksum []byte, err error) {
	hasher := sha256.New()
	n, err := io.CopyN(io.MultiWriter(w, hasher), r, int64(size))
	if err != nil {
		return nil, err
	}
	if n != int64(size) {
		return hasher.Sum(nil), io.ErrUnexpectedEOF
	}
	return hasher.Sum(nil), nil
}

func SendStatus(w io.Writer, status byte) error {
	_, err := w.Write([]byte{status})
	return err
}

func ReadStatus(r io.Reader) (byte, error) {
	b := make([]byte, 1)
	_, err := io.ReadFull(r, b)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func WriteMessageType(w io.Writer, msgType byte) error {
	_, err := w.Write([]byte{msgType})
	return err
}

func ReadMessageType(r io.Reader) (byte, error) {
	b := make([]byte, 1)
	_, err := io.ReadFull(r, b)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func SendCodeResponse(w io.Writer, status byte, code string) error {
	if len(code) != CodeLength {
		return nil
	}
	if _, err := w.Write([]byte{status}); err != nil {
		return err
	}
	if status == StatusOK {
		_, err := w.Write([]byte(code))
		return err
	}
	return nil
}

func ReadCodeResponse(r io.Reader) (status byte, code string, err error) {
	status, err = ReadStatus(r)
	if err != nil || status != StatusOK {
		return status, "", err
	}
	b := make([]byte, CodeLength)
	if _, err = io.ReadFull(r, b); err != nil {
		return 0, "", err
	}
	return status, string(b), nil
}

func WriteDownloadRequest(w io.Writer, code string) error {
	if len(code) != CodeLength {
		return nil
	}
	_, err := w.Write([]byte(code))
	return err
}

func ReadDownloadRequest(r io.Reader) (string, error) {
	b := make([]byte, CodeLength)
	_, err := io.ReadFull(r, b)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func SendFileFromData(w io.Writer, name string, data []byte) ([]byte, error) {
	return SendFile(w, name, bytes.NewReader(data), int64(len(data)), nil)
}

func WriteEncryptedUpload(w io.Writer, code string, name string, plaintextChecksum []byte, nonce, sealed []byte, progress ProgressFunc) error {
	if len(code) != CodeLength || len(plaintextChecksum) != sha256.Size || len(nonce) != nonceSize {
		return nil
	}
	if _, err := w.Write([]byte(code)); err != nil {
		return err
	}
	nameBytes := []byte(name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return err
	}
	if _, err := w.Write(plaintextChecksum); err != nil {
		return err
	}
	if _, err := w.Write(nonce); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(len(sealed))); err != nil {
		return err
	}
	total := int64(len(sealed))
	var sent int64
	for sent < total {
		n := sendChunkSize
		if total-sent < int64(n) {
			n = int(total - sent)
		}
		if _, err := w.Write(sealed[sent : sent+int64(n)]); err != nil {
			return err
		}
		sent += int64(n)
		if progress != nil {
			progress(sent, total)
		}
	}
	return nil
}

// ReadSecureUpload odczytuje body secure uploadu (bez kodu): name, checksum, nonce, sealed. Limit: maxSealed.
func ReadSecureUpload(r io.Reader, maxSealed int64) (name string, plaintextChecksum []byte, nonce, sealed []byte, err error) {
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return "", nil, nil, nil, err
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return "", nil, nil, nil, err
	}
	name = string(nameBuf)
	plaintextChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, plaintextChecksum); err != nil {
		return "", nil, nil, nil, err
	}
	nonce = make([]byte, nonceSize)
	if _, err = io.ReadFull(r, nonce); err != nil {
		return "", nil, nil, nil, err
	}
	var sealedLen uint64
	if err = binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
		return "", nil, nil, nil, err
	}
	if maxSealed > 0 && sealedLen > uint64(maxSealed) {
		return "", nil, nil, nil, ErrBlobTooLarge
	}
	sealed = make([]byte, sealedLen)
	if _, err = io.ReadFull(r, sealed); err != nil {
		return "", nil, nil, nil, err
	}
	return name, plaintextChecksum, nonce, sealed, nil
}

func ReadEncryptedUpload(r io.Reader, maxSealed int64) (code string, name string, plaintextChecksum []byte, nonce, sealed []byte, err error) {
	codeBuf := make([]byte, CodeLength)
	if _, err = io.ReadFull(r, codeBuf); err != nil {
		return "", "", nil, nil, nil, err
	}
	code = string(codeBuf)
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return "", "", nil, nil, nil, err
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return "", "", nil, nil, nil, err
	}
	name = string(nameBuf)
	plaintextChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, plaintextChecksum); err != nil {
		return "", "", nil, nil, nil, err
	}
	nonce = make([]byte, nonceSize)
	if _, err = io.ReadFull(r, nonce); err != nil {
		return "", "", nil, nil, nil, err
	}
	var sealedLen uint64
	if err = binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
		return "", "", nil, nil, nil, err
	}
	if maxSealed > 0 && sealedLen > uint64(maxSealed) {
		return "", "", nil, nil, nil, ErrBlobTooLarge
	}
	sealed = make([]byte, sealedLen)
	if _, err = io.ReadFull(r, sealed); err != nil {
		return "", "", nil, nil, nil, err
	}
	return code, name, plaintextChecksum, nonce, sealed, nil
}

func WriteEncryptedUploadChunked(w io.Writer, code string, name string, totalPlainLen int64, numChunks uint32, plaintextChecksum []byte, getChunk func() ([]byte, error), progress ProgressFunc) error {
	if len(code) != CodeLength || len(plaintextChecksum) != sha256.Size {
		return nil
	}
	if _, err := w.Write([]byte(code)); err != nil {
		return err
	}
	nameBytes := []byte(name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(totalPlainLen)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, numChunks); err != nil {
		return err
	}
	if _, err := w.Write(plaintextChecksum); err != nil {
		return err
	}
	var sent int64
	for {
		chunk, err := getChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		nonce, sealed, encErr := encryptChunk(code, chunk)
		if encErr != nil {
			return encErr
		}
		if _, err := w.Write(nonce); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint32(len(sealed))); err != nil {
			return err
		}
		if _, err := w.Write(sealed); err != nil {
			return err
		}
		sent += int64(len(chunk))
		if progress != nil {
			progress(sent, totalPlainLen)
		}
	}
	return nil
}

func ReadEncryptedUploadChunked(r io.Reader, maxTotalPlain int64) (code string, name string, plaintextChecksum []byte, chunks []EncryptedChunk, err error) {
	codeBuf := make([]byte, CodeLength)
	if _, err = io.ReadFull(r, codeBuf); err != nil {
		return "", "", nil, nil, err
	}
	code = string(codeBuf)
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return "", "", nil, nil, err
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return "", "", nil, nil, err
	}
	name = string(nameBuf)
	var totalPlainLen uint64
	if err = binary.Read(r, binary.BigEndian, &totalPlainLen); err != nil {
		return "", "", nil, nil, err
	}
	if maxTotalPlain > 0 && int64(totalPlainLen) > maxTotalPlain {
		return "", "", nil, nil, ErrBlobTooLarge
	}
	var numChunks uint32
	if err = binary.Read(r, binary.BigEndian, &numChunks); err != nil {
		return "", "", nil, nil, err
	}
	plaintextChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, plaintextChecksum); err != nil {
		return "", "", nil, nil, err
	}
	chunks = make([]EncryptedChunk, 0, numChunks)
	for i := uint32(0); i < numChunks; i++ {
		var c EncryptedChunk
		if _, err = io.ReadFull(r, c.Nonce[:]); err != nil {
			return "", "", nil, nil, err
		}
		var sealedLen uint32
		if err = binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
			return "", "", nil, nil, err
		}
		c.Sealed = make([]byte, sealedLen)
		if _, err = io.ReadFull(r, c.Sealed); err != nil {
			return "", "", nil, nil, err
		}
		chunks = append(chunks, c)
	}
	return code, name, plaintextChecksum, chunks, nil
}

func WriteEncryptedBlob(w io.Writer, name string, plaintextChecksum []byte, nonce, sealed []byte, progress ProgressFunc) error {
	nameBytes := []byte(name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return err
	}
	if _, err := w.Write(plaintextChecksum); err != nil {
		return err
	}
	if _, err := w.Write(nonce); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint64(len(sealed))); err != nil {
		return err
	}
	total := int64(len(sealed))
	var sent int64
	for sent < total {
		n := sendChunkSize
		if total-sent < int64(n) {
			n = int(total - sent)
		}
		if _, err := w.Write(sealed[sent : sent+int64(n)]); err != nil {
			return err
		}
		sent += int64(n)
		if progress != nil {
			progress(sent, total)
		}
	}
	return nil
}

func WriteEncryptedBlobChunked(w io.Writer, name string, plaintextChecksum []byte, chunks []EncryptedChunk) error {
	nameBytes := []byte(name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return err
	}
	var totalPlainLen uint64
	for _, c := range chunks {
		if len(c.Sealed) >= 16 {
			totalPlainLen += uint64(len(c.Sealed) - 16)
		}
	}
	if err := binary.Write(w, binary.BigEndian, totalPlainLen); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(chunks))); err != nil {
		return err
	}
	if _, err := w.Write(plaintextChecksum); err != nil {
		return err
	}
	for _, c := range chunks {
		if _, err := w.Write(c.Nonce[:]); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint32(len(c.Sealed))); err != nil {
			return err
		}
		if _, err := w.Write(c.Sealed); err != nil {
			return err
		}
	}
	return nil
}

func ReadEncryptedBlobChunkedHeader(r io.Reader) (name string, totalPlainLen uint64, numChunks uint32, plaintextChecksum []byte, err error) {
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return "", 0, 0, nil, err
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return "", 0, 0, nil, err
	}
	name = string(nameBuf)
	if err = binary.Read(r, binary.BigEndian, &totalPlainLen); err != nil {
		return "", 0, 0, nil, err
	}
	if err = binary.Read(r, binary.BigEndian, &numChunks); err != nil {
		return "", 0, 0, nil, err
	}
	plaintextChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, plaintextChecksum); err != nil {
		return "", 0, 0, nil, err
	}
	return name, totalPlainLen, numChunks, plaintextChecksum, nil
}

func ReadEncryptedBlobNextChunk(r io.Reader, code string) (plaintext []byte, err error) {
	var nonce [12]byte
	if _, err = io.ReadFull(r, nonce[:]); err != nil {
		return nil, err
	}
	var sealedLen uint32
	if err = binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
		return nil, err
	}
	sealed := make([]byte, sealedLen)
	if _, err = io.ReadFull(r, sealed); err != nil {
		return nil, err
	}
	return decryptChunk(code, nonce[:], sealed)
}

func ReadEncryptedBlob(r io.Reader, progress ProgressFunc) (name string, plaintextChecksum []byte, nonce, sealed []byte, err error) {
	var nameLen uint16
	if err = binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		return "", nil, nil, nil, err
	}
	nameBuf := make([]byte, nameLen)
	if _, err = io.ReadFull(r, nameBuf); err != nil {
		return "", nil, nil, nil, err
	}
	name = string(nameBuf)
	plaintextChecksum = make([]byte, sha256.Size)
	if _, err = io.ReadFull(r, plaintextChecksum); err != nil {
		return "", nil, nil, nil, err
	}
	nonce = make([]byte, nonceSize)
	if _, err = io.ReadFull(r, nonce); err != nil {
		return "", nil, nil, nil, err
	}
	var sealedLen uint64
	if err = binary.Read(r, binary.BigEndian, &sealedLen); err != nil {
		return "", nil, nil, nil, err
	}
	sealed = make([]byte, 0, sealedLen)
	total := int64(sealedLen)
	var read int64
	for read < total {
		n := sendChunkSize
		if total-read < int64(n) {
			n = int(total - read)
		}
		chunk := make([]byte, n)
		if _, err = io.ReadFull(r, chunk); err != nil {
			return "", nil, nil, nil, err
		}
		sealed = append(sealed, chunk...)
		read += int64(n)
		if progress != nil {
			progress(read, total)
		}
	}
	return name, plaintextChecksum, nonce, sealed, nil
}

func checksumEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
