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
	MsgUpload   = 'U'
	MsgDownload = 'D'
)

const (
	StatusOK            = 0
	StatusChecksumError = 1
	StatusError         = 2
	StatusNotFound      = 3
)

const CodeLength = 6

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

func WriteEncryptedBlob(w io.Writer, name string, plaintextChecksum []byte, nonce, sealed []byte) error {
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
	total := len(sealed)
	for sent := 0; sent < total; {
		n := sendChunkSize
		if total-sent < n {
			n = total - sent
		}
		if _, err := w.Write(sealed[sent : sent+n]); err != nil {
			return err
		}
		sent += n
	}
	return nil
}

func ReadEncryptedBlob(r io.Reader) (name string, plaintextChecksum []byte, nonce, sealed []byte, err error) {
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
	sealed = make([]byte, sealedLen)
	if _, err = io.ReadFull(r, sealed); err != nil {
		return "", nil, nil, nil, err
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
