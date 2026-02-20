package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

const gcmNonceSize = 12

var keySalt = []byte("tcpraw-v1")

func deriveKey(code string) []byte {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write(keySalt)
	return h.Sum(nil)
}

func encryptWithCode(code string, plaintext []byte) (nonce, sealed []byte, err error) {
	key := deriveKey(code)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	sealed = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, sealed, nil
}

func decryptWithCode(code string, nonce, sealed []byte) (plaintext []byte, err error) {
	key := deriveKey(code)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcmNonceSize {
		return nil, errors.New("invalid nonce size")
	}
	plaintext, err = gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func encryptChunk(code string, plaintext []byte) (nonce, sealed []byte, err error) {
	return encryptWithCode(code, plaintext)
}

func decryptChunk(code string, nonce, sealed []byte) (plaintext []byte, err error) {
	return decryptWithCode(code, nonce, sealed)
}

const SecureKeySize = 32

func encryptWithKey(key []byte, plaintext []byte) (nonce, sealed []byte, err error) {
	if len(key) != SecureKeySize {
		return nil, nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	sealed = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, sealed, nil
}

func decryptWithKey(key, nonce, sealed []byte) (plaintext []byte, err error) {
	if len(key) != SecureKeySize {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcmNonceSize {
		return nil, errors.New("invalid nonce size")
	}
	plaintext, err = gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}
