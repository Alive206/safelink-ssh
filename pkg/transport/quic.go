// Package transport provides QUIC-based transport for SafeLink VPN tunnels.
// Anti-fingerprinting: ALPN is set to "h3" (HTTP/3) so traffic appears
// indistinguishable from standard browser traffic to DPI.
package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	randv2 "math/rand/v2"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

const alpn = "h3"
const defaultSNI = "gateway.icloud.com"

// TLSOpts holds optional TLS parameters for QUIC connections.
type TLSOpts struct {
	CertFile  string
	KeyFile   string
	SNI       string
	PinSHA256 string
}

// ServerTLS returns a TLS config for a QUIC server.
func ServerTLS(opts TLSOpts) (*tls.Config, error) {
	cert, err := loadOrGenerateCert(opts.CertFile, opts.KeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{alpn},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// ClientTLS returns a TLS config for QUIC client connections.
func ClientTLS(opts TLSOpts) *tls.Config {
	sni := opts.SNI
	if sni == "" {
		sni = defaultSNI
	}
	cfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		NextProtos:         []string{alpn},
		MinVersion:         tls.VersionTLS13,
	}
	if opts.PinSHA256 != "" {
		pin := opts.PinSHA256
		cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no server certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("parse cert: %w", err)
			}
			spkiHash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			got := hex.EncodeToString(spkiHash[:])
			if got != pin {
				return fmt.Errorf("certificate pin mismatch: got %s, want %s", got, pin)
			}
			return nil
		}
	}
	return cfg
}

func randomKeepAlive() time.Duration {
	return time.Duration(20+randv2.IntN(21)) * time.Second
}

// DialQUIC opens a QUIC connection to addr.
func DialQUIC(ctx context.Context, addr string, opts TLSOpts) (quic.Connection, error) {
	conn, err := quic.DialAddr(ctx, addr, ClientTLS(opts), &quic.Config{
		KeepAlivePeriod: randomKeepAlive(),
	})
	if err != nil {
		return nil, fmt.Errorf("quic dial: %w", err)
	}
	return conn, nil
}

// ListenQUIC starts a QUIC listener on addr.
func ListenQUIC(addr string, opts TLSOpts) (*quic.Listener, error) {
	tlsCfg, err := ServerTLS(opts)
	if err != nil {
		return nil, fmt.Errorf("server tls: %w", err)
	}
	ln, err := quic.ListenAddr(addr, tlsCfg, &quic.Config{
		KeepAlivePeriod: randomKeepAlive(),
	})
	if err != nil {
		return nil, fmt.Errorf("quic listen: %w", err)
	}
	return ln, nil
}


// QUICStreamConn wraps a quic.Stream as a net.Conn.
type QUICStreamConn struct {
	quic.Stream
	Local  net.Addr
	Remote net.Addr
}

func (c *QUICStreamConn) LocalAddr() net.Addr  { return c.Local }
func (c *QUICStreamConn) RemoteAddr() net.Addr { return c.Remote }

// WrapStream wraps a quic.Stream as a net.Conn.
func WrapStream(s quic.Stream, local, remote net.Addr) *QUICStreamConn {
	return &QUICStreamConn{Stream: s, Local: local, Remote: remote}
}

// CertFingerprint returns the hex-encoded SHA-256 of a certificate's SPKI.
func CertFingerprint(certDER []byte) (string, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(h[:]), nil
}

func loadOrGenerateCert(certFile, keyFile string) (tls.Certificate, error) {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load tls keypair: %w", err)
		}
		return cert, nil
	}
	return generateCert(), nil
}

func generateCert() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("generate ecdsa key: %v", err))
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   "gateway.icloud.com",
			Organization: []string{"Apple Inc."},
		},
		DNSNames:              []string{"gateway.icloud.com", "*.icloud.com"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(fmt.Sprintf("create cert: %v", err))
	}
	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}
