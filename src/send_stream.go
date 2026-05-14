package main

import (
	"encoding/binary"
	"io"
	"os"
)

// sendChunkedFromFile sends encrypted chunks from the .dat file in chunked protocol format.
func sendChunkedFromFile(w io.Writer, dataPath string, blob *StoredBlob) error {
	df, err := os.Open(dataPath)
	if err != nil {
		return err
	}
	defer df.Close()

	nameBytes := []byte(blob.Name)
	if len(nameBytes) > 0xFFFF {
		nameBytes = nameBytes[:0xFFFF]
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(nameBytes))); err != nil {
		return err
	}
	if _, err := w.Write(nameBytes); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, blob.TotalPlainLen); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, blob.NumChunks); err != nil {
		return err
	}
	if _, err := w.Write(blob.PlaintextChecksum); err != nil {
		return err
	}

	for i := uint32(0); i < blob.NumChunks; i++ {
		var header [16]byte
		if _, err := io.ReadFull(df, header[:16]); err != nil {
			return err
		}
		if _, err := w.Write(header[:16]); err != nil {
			return err
		}
		sealedLen := binary.BigEndian.Uint32(header[12:16])
		sealed := make([]byte, sealedLen)
		if _, err := io.ReadFull(df, sealed); err != nil {
			return err
		}
		if _, err := w.Write(sealed); err != nil {
			return err
		}
	}
	return nil
}

