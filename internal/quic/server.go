package quic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

type Server struct {
	addr      string
	transport *quic.Transport
	listener  *quic.Listener
	mu        sync.RWMutex
	sessions  map[string]*Session
	onConnect func(streamID string) error
	onData    func(streamID string, data []byte)
}

func NewServer(addr string) *Server {
	return &Server{
		addr:     addr,
		sessions: make(map[string]*Session),
	}
}

func (s *Server) SetOnConnect(f func(streamID string) error) {
	s.onConnect = f
}

func (s *Server) SetOnData(f func(streamID string, data []byte)) {
	s.onData = f
}

func (s *Server) Start(ctx context.Context) error {
	udpAddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return fmt.Errorf("resolve udp: %w", err)
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}

	tlsConfig := generateTLSConfig()

	s.transport = &quic.Transport{Conn: udpConn}
	ln, err := s.transport.Listen(tlsConfig, nil)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln

	go s.acceptLoop(ctx)
	return nil
}

func (s *Server) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.listener.Accept(ctx)
		if err != nil {
			return
		}

		session := NewSession(conn, s)
		s.mu.Lock()
		s.sessions[session.ID()] = session
		s.mu.Unlock()

		go session.Run(ctx)
	}
}

func (s *Server) RemoveSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *Server) GetSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

func (s *Server) Close() error {
	if s.listener != nil {
		s.listener.Close()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	return nil
}

func generateTLSConfig() *tls.Config {
	certPEM, keyPEM, err := generateCert()
	if err != nil {
		panic(err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"duckstream"},
	}
}

func generateCert() ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"duckstream"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return certPEM, keyPEM, nil
}

type Session struct {
	conn    *quic.Conn
	server  *Server
	id      string
	mu      sync.RWMutex
	streams map[quic.StreamID]*Stream
}

func NewSession(conn *quic.Conn, server *Server) *Session {
	return &Session{
		conn:    conn,
		server:  server,
		id:      conn.RemoteAddr().String(),
		streams: make(map[quic.StreamID]*Stream),
	}
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Run(ctx context.Context) {
	defer s.server.RemoveSession(s.ID())

	for {
		stream, err := s.conn.AcceptStream(ctx)
		if err != nil {
			return
		}

		s.mu.Lock()
		s.streams[stream.StreamID()] = NewStream(stream)
		s.mu.Unlock()

		if s.server.onData != nil {
			go s.handleStream(stream)
		}
	}
}

func (s *Session) handleStream(stream *quic.Stream) {
	defer stream.Close()

	buf := make([]byte, 4096)
	for {
		n, err := (*stream).Read(buf)
		if err != nil {
			return
		}
		if s.server.onData != nil {
			s.server.onData(fmt.Sprintf("%d", stream.StreamID()), buf[:n])
		}
	}
}

func (s *Session) OpenStreamSync(ctx context.Context) (*quic.Stream, error) {
	return s.conn.OpenStreamSync(ctx)
}

func (s *Session) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

func (s *Session) Close() error {
	return s.conn.CloseWithError(0, "")
}

func (s *Session) SendToStream(streamID string, data []byte) error {
	s.mu.RLock()
	stream, ok := s.streams[quic.StreamID(0)]
	s.mu.RUnlock()

	if !ok {
		qstream, err := s.conn.OpenStreamSync(context.Background())
		if err != nil {
			return err
		}
		stream = NewStream(qstream)
		s.mu.Lock()
		s.streams[qstream.StreamID()] = stream
		s.mu.Unlock()
	}

	_, err := stream.Write(data)
	return err
}
