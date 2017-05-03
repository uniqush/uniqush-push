package common

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/ioutil"
)

// LoadPKCS8Key reads PEM-formatted PKCS8 private key from file
// Returns error if fail to read key from provided path
func LoadPKCS8Key(keyFile string) (interface{}, error) {
	keyPem, err := ioutil.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(keyPem)
	if block == nil {
		return nil, errors.New("No PEM data found")
	}
	return x509.ParsePKCS8PrivateKey(block.Bytes)
}
