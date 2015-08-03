package tests

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"io/ioutil"
	"testing"
	"time"

	"github.com/cloudflare/gokeyless/client"
)

const (
	tlsCert   = "testdata/tls.pem"
	tlsKey    = "testdata/tls.key"
	network   = "tcp"
	localAddr = "localhost:7777"
)

func LoadX509KeyPair(c *client.Client, serverAddr, certFile string) (cert tls.Certificate, err error) {
	fail := func(err error) (tls.Certificate, error) { return tls.Certificate{}, err }
	var certPEMBlock []byte
	var certDERBlock *pem.Block

	if certPEMBlock, err = ioutil.ReadFile(certFile); err != nil {
		return fail(err)
	}

	for {
		certDERBlock, certPEMBlock = pem.Decode(certPEMBlock)
		if certDERBlock == nil {
			break
		}

		if certDERBlock.Type == "CERTIFICATE" {
			cert.Certificate = append(cert.Certificate, certDERBlock.Bytes)
		}
	}

	if len(cert.Certificate) == 0 {
		return fail(errors.New("crypto/tls: failed to parse certificate PEM data"))
	}

	if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
		return fail(err)
	}

	cert.PrivateKey, err = c.RegisterCert(serverAddr, cert.Leaf)
	if err != nil {
		return fail(err)
	}

	return cert, nil
}

func serverFunc(conn *tls.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	io.Copy(conn, conn)
}

func clientFunc(conn *tls.Conn) error {
	defer conn.Close()
	if !conn.ConnectionState().HandshakeComplete {
		return errors.New("handshake didn't complete")
	}

	input := []byte("Hello World!")
	if _, err := conn.Write(input); err != nil {
		return err
	}

	output, err := ioutil.ReadAll(conn)
	if err != nil {
		return err
	}
	if bytes.Compare(input, output) != 0 {
		return errors.New("input and output do not match")
	}
	return nil
}

func TestTLSProxy(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	cert, err := LoadX509KeyPair(c, serverAddr, tlsCert)
	if err != nil {
		t.Fatal(err)
	}
	c.RegisterCert(serverAddr, cert.Leaf)

	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName:   cert.Leaf.Subject.CommonName,
	}

	l, err := tls.Listen(network, localAddr, serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for c, err := l.Accept(); err == nil; c, err = l.Accept() {
			go serverFunc(c.(*tls.Conn))
		}
	}()

	pemKey, err := ioutil.ReadFile(tlsKey)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(pemKey)
	rsaKey, err := x509.ParsePKCS1PrivateKey(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterKey(rsaKey); err != nil {
		t.Fatal(err)
	}

	clientConfig := &tls.Config{
		ServerName: serverConfig.Certificates[0].Leaf.Subject.CommonName,
		RootCAs:    x509.NewCertPool(),
	}
	clientConfig.RootCAs.AddCert(cert.Leaf)

	conn, err := tls.Dial(network, localAddr, clientConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err = clientFunc(conn); err != nil {
		t.Fatal(err)
	}
}