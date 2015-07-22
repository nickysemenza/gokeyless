package client

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"

	"github.com/cloudflare/gokeyless"
)

// Client is a Keyless Client capable of connecting to servers and performing keyless operations.
type Client struct {
	// Config is initialized with the client auth configuration used for communicating with keyless servers.
	Config *tls.Config
	// Dialer used to manage connections.
	Dialer *net.Dialer
	// Log used to output informational data.
	Log *log.Logger
	// conns maps keyless servers to any open connections to them.
	conns map[string]*gokeyless.Conn
	// allServers maps all known certificate SKIs to their keyless servers.
	allServers map[gokeyless.SKI][]string
}

// NewClient prepares a TLS client capable of connecting to keyservers.
func NewClient(certFile, keyFile, caFile string, logOut io.Writer) (*Client, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	pemCerts, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}

	keyserverRoot := x509.NewCertPool()
	if !keyserverRoot.AppendCertsFromPEM(pemCerts) {
		return nil, errors.New("gokeyless/client: failed to read keyserver CA from PEM")
	}

	return &Client{
		Config: &tls.Config{
			RootCAs:      keyserverRoot,
			Certificates: []tls.Certificate{cert},
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			},
		},
		Dialer:     &net.Dialer{},
		Log:        log.New(logOut, "[client] ", log.LstdFlags),
		conns:      make(map[string]*gokeyless.Conn),
		allServers: make(map[gokeyless.SKI][]string),
	}, nil
}

// Dial retuns a (reused/reusable) connection to a keyless server.
func (c *Client) Dial(server string) (*gokeyless.Conn, error) {
	if c.Config == nil {
		return nil, errors.New("gokeyless/client: TLS client has not yet been initialized with certificate and keyserver CA")
	}

	if conn, ok := c.conns[server]; ok && conn.IsOpen {
		return conn, nil
	} else if ok {
		delete(c.conns, server)
	}

	c.Log.Printf("Dialing %s\n", server)
	conn, err := tls.Dial("tcp", server, c.Config)
	if err != nil {
		return nil, err
	}

	c.conns[server] = gokeyless.NewConn(conn)
	return c.conns[server], nil
}

// DialAny smartly chooses one of the keyless servers given. (Opting to reuse an existing connection if possible)
func (c *Client) DialAny(ski gokeyless.SKI) (*gokeyless.Conn, error) {
	servers := c.allServers[ski]
	if len(servers) == 0 {
		return nil, errors.New("no servers given")
	}

	var existing []*gokeyless.Conn
	for _, server := range servers {
		if conn, ok := c.conns[server]; ok {
			existing = append(existing, conn)
		}
	}
	// choose from existing connections at random
	if len(existing) > 0 {
		return existing[rand.Intn(len(existing))], nil
	}

	// choose from possible servers at random until a connection can be established.
	for len(servers) > 0 {
		n := rand.Intn(len(servers))
		conn, err := c.Dial(servers[n])
		if err == nil {
			return conn, nil
		}
		log.Printf("Couldn't dial server %s: %v", servers[n], err)
		servers = append(servers[:n], servers[n+1:]...)
	}
	return nil, errors.New("couldn't dial any of the servers given")
}

// registerSKI associates the SKI of a public key with a particular keyserver.
func (c *Client) registerSKI(server string, ski gokeyless.SKI) {
	c.Log.Printf("Registering key @ %s\t%x", server, ski)
	c.allServers[ski] = append(c.allServers[ski], server)
}

// RegisterPublicKey SKIs and registers a public key as being held by a server.
func (c *Client) RegisterPublicKey(server string, pub crypto.PublicKey) (*PrivateKey, error) {
	ski, err := gokeyless.GetSKI(pub)
	if err != nil {
		return nil, err
	}
	c.registerSKI(server, ski)

	digest, _ := gokeyless.GetDigest(pub)

	return &PrivateKey{
		public: pub,
		ski:    ski,
		digest: digest,
		client: c,
	}, nil
}

// RegisterCert SKIs the public key contained in a certificate and associates it with a particular keyserver.
func (c *Client) RegisterCert(server string, cert *x509.Certificate) (*PrivateKey, error) {
	return c.RegisterPublicKey(server, cert.PublicKey)
}