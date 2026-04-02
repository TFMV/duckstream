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
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/duckstream/duckstream/internal/config"
)

type Server struct {
	addr      string
	transport *quic.Transport
	listener  *quic.Listener
	mu        sync.RWMutex
	sessions  map[string]*Session
	onConnect func(streamID string) error
	onData    func(streamID string, data []byte)

	maxClients          int
	activeClients       atomic.Int64
	maxStreamsPerClient int
}

func NewServer(addr string, cfg *config.Config) *Server {
	return &Server{
		addr:                addr,
		sessions:            make(map[string]*Session),
		maxClients:          cfg.MaxClients,
		maxStreamsPerClient: cfg.MaxStreamsPerClient,
	}
}

func (s *Server) CanAccept() bool {
	return int(s.activeClients.Load()) < s.maxClients
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

		if !s.CanAccept() {
			_ = conn.CloseWithError(0, "max clients reached")
			continue
		}

		s.activeClients.Add(1)

		session := NewSession(conn, s, s.maxStreamsPerClient)
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
	s.activeClients.Add(-1)
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

func (s *Server) ActiveClients() int {
	return int(s.activeClients.Load())
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
	conn          *quic.Conn
	server        *Server
	id            string
	mu            sync.RWMutex
	streams       map[string]*Stream
	maxStreams    int
	activeStreams atomic.Int64
}

func NewSession(conn *quic.Conn, server *Server, maxStreams int) *Session {
	return &Session{
		conn:       conn,
		server:     server,
		id:         conn.RemoteAddr().String(),
		streams:    make(map[string]*Stream),
		maxStreams: maxStreams,
	}
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) CanOpenStream() bool {
	return int(s.activeStreams.Load()) < s.maxStreams
}

func (s *Session) Run(ctx context.Context) {
	defer func() {
		s.server.RemoveSession(s.ID())
		s.server.activeClients.Add(-1)
	}()

	for {
		stream, err := s.conn.AcceptStream(ctx)
		if err != nil {
			return
		}

		s.mu.Lock()
		s.streams[fmt.Sprintf("%d", stream.StreamID())] = NewStream(stream)
		s.mu.Unlock()

		if s.server.onData != nil {
			go s.handleStream(stream)
		}
	}
}

func (s *Session) handleStream(stream *quic.Stream) {
	defer func() {
		stream.Close()
		s.activeStreams.Add(-1)
	}()

	s.activeStreams.Add(1)

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
	if !s.CanOpenStream() {
		return nil, fmt.Errorf("max streams per client reached")
	}
	s.activeStreams.Add(1)
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
	stream, ok := s.streams[streamID]
	s.mu.RUnlock()

	if !ok {
		qstream, err := s.OpenStreamSync(context.Background())
		if err != nil {
			return err
		}
		stream = NewStream(qstream)
		s.mu.Lock()
		s.streams[fmt.Sprintf("%d", qstream.StreamID())] = stream
		s.mu.Unlock()
	}

	_, err := stream.Write(data)
	return err
}

func (s *Session) SendToQuery(queryID string, data []byte) error {
	if !s.CanOpenStream() {
		return fmt.Errorf("max streams per client reached")
	}

	streamKey := "query:" + queryID

	s.mu.RLock()
	stream, ok := s.streams[streamKey]
	s.mu.RUnlock()

	if !ok {
		qstream, err := s.OpenStreamSync(context.Background())
		if err != nil {
			return err
		}
		stream = NewStream(qstream)
		s.mu.Lock()
		s.streams[streamKey] = stream
		s.mu.Unlock()
	}

	_, err := stream.Write(data)
	return err
}
