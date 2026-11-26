package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

const (
	defaultRunDuration = 30 * time.Minute
	readTimeout        = 10 * time.Second
	// runTimeout covers the engine's image pull, which may take five minutes.
	runTimeout     = 6 * time.Minute
	reclaimTimeout = 30 * time.Second
)

// Manager is the part of lease.Manager the control server drives.
type Manager interface {
	ReceiveOffer(ctx context.Context, offer lease.Offer) error
	WaitForOffer(ctx context.Context, leaseID string) error
	Drain(ctx context.Context) error
	Resume(ctx context.Context) error
	Reclaim(ctx context.Context) error
	State() lease.State
	ActiveLease() (lease.Active, bool)
}

// Server answers gpu-ctl commands for one lease manager.
type Server struct {
	manager           Manager
	recorder          *Recorder
	images            []allowlist.Entry
	now               func() time.Time
	logger            *log.Logger
	jobs              sync.WaitGroup
	coordinatorStatus atomic.Value // stores coordinator.ProviderStatus
}

// NewServer creates a control server. recorder must be the lease.Coordinator
// the manager reports to, so status can show the last result.
func NewServer(manager Manager, recorder *Recorder, images []allowlist.Entry, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Server{manager: manager, recorder: recorder, images: images, now: time.Now, logger: logger}
}

// SetCoordinatorStatus records the latest status reported by the coordinator.
func (server *Server) SetCoordinatorStatus(status coordinator.ProviderStatus) {
	server.coordinatorStatus.Store(status)
}

// Serve answers commands until ctx is cancelled, then waits for open
// connections and running leases to finish. Cancelling ctx also cancels
// running leases, which kills their workloads.
func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		listener.Close()
	}()

	var connections sync.WaitGroup
	defer server.jobs.Wait()
	defer connections.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept control connection: %w", err)
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			server.handle(ctx, connection)
		}()
	}
}

func (server *Server) handle(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(readTimeout))

	var request Request
	if err := json.NewDecoder(io.LimitReader(connection, maxRequestBytes)).Decode(&request); err != nil {
		server.reply(connection, Response{Error: "malformed command: " + err.Error()})
		return
	}
	switch request.Command {
	case CommandRun:
		connection.SetWriteDeadline(time.Now().Add(runTimeout))
	case CommandReclaim:
		connection.SetWriteDeadline(time.Now().Add(reclaimTimeout + readTimeout))
	}
	server.reply(connection, server.dispatch(ctx, request))
}

func (server *Server) reply(connection net.Conn, response Response) {
	if err := json.NewEncoder(connection).Encode(response); err != nil {
		server.logger.Printf("control: reply failed: %v", err)
	}
}

func (server *Server) dispatch(ctx context.Context, request Request) Response {
	var response Response
	var err error
	switch request.Command {
	case CommandStatus:
	case CommandImages:
		response.Images = server.images
	case CommandDrain:
		err = server.manager.Drain(ctx)
		server.logIf(err == nil, "control: drain requested")
	case CommandResume:
		err = server.manager.Resume(ctx)
		server.logIf(err == nil, "control: resume requested")
	case CommandReclaim:
		reclaimCtx, cancel := context.WithTimeout(ctx, reclaimTimeout)
		err = server.manager.Reclaim(reclaimCtx)
		cancel()
		server.logIf(err == nil, "control: workload reclaimed")
	case CommandRun:
		response.LeaseID, err = server.run(ctx, request)
	default:
		err = fmt.Errorf("unknown command %q", request.Command)
	}
	if err != nil {
		return Response{Error: err.Error()}
	}

	response.OK = true
	response.State = server.manager.State()
	if status, ok := server.coordinatorStatus.Load().(coordinator.ProviderStatus); ok {
		response.CoordinatorStatus = status
	}
	if active, ok := server.manager.ActiveLease(); ok {
		response.Lease = &Lease{ID: active.ID, ImageDigest: active.ImageDigest, ExpiresAt: active.ExpiresAt}
	}
	if result, ok := server.recorder.LastResult(); ok {
		response.LastResult = &Result{LeaseID: result.LeaseID, Outcome: result.Outcome, ExitCode: result.ExitCode}
	}
	return response
}

func (server *Server) logIf(condition bool, message string) {
	if condition {
		server.logger.Print(message)
	}
}

// run offers the chosen image to the lease manager as a local lease, then
// collects its result in the background.
func (server *Server) run(ctx context.Context, request Request) (string, error) {
	if request.Digest == "" {
		return "", errors.New("run needs an image digest")
	}
	duration := defaultRunDuration
	if request.Seconds != 0 {
		duration = time.Duration(request.Seconds) * time.Second
	}
	if duration <= 0 || duration > MaxRunDuration {
		return "", fmt.Errorf("lease length must be between 1s and %s", MaxRunDuration)
	}
	leaseID, err := newLeaseID()
	if err != nil {
		return "", err
	}

	offer := lease.Offer{ID: leaseID, ImageDigest: request.Digest, ExpiresAt: server.now().Add(duration)}
	if err := server.manager.ReceiveOffer(ctx, offer); err != nil {
		var rejection *lease.OfferRejectedError
		if errors.As(err, &rejection) {
			return "", fmt.Errorf("node rejected the job: %s", rejection.Reason)
		}
		return "", err
	}
	server.logger.Printf("control: started lease %s for %s", leaseID, request.Digest)

	server.jobs.Add(1)
	go func() {
		defer server.jobs.Done()
		if err := server.manager.WaitForOffer(ctx, leaseID); err != nil && !errors.Is(err, lease.ErrNoActiveWorkload) {
			server.logger.Printf("control: lease %s: %v", leaseID, err)
		}
	}()
	return leaseID, nil
}

func newLeaseID() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate lease ID: %w", err)
	}
	return "local-" + hex.EncodeToString(random[:]), nil
}

// Recorder passes lease reports through to another coordinator and remembers
// the most recent result for status.
type Recorder struct {
	next lease.Coordinator
	mu   sync.Mutex
	last *lease.Result
}

// NewRecorder wraps the coordinator the lease manager reports to.
func NewRecorder(next lease.Coordinator) *Recorder {
	return &Recorder{next: next}
}

// ReportAccepted forwards an acceptance.
func (recorder *Recorder) ReportAccepted(ctx context.Context, leaseID string) error {
	return recorder.next.ReportAccepted(ctx, leaseID)
}

// ReportRejected forwards a rejection.
func (recorder *Recorder) ReportRejected(ctx context.Context, leaseID string, reason lease.RejectionReason) error {
	return recorder.next.ReportRejected(ctx, leaseID, reason)
}

// ReportResult records and forwards a finished lease.
func (recorder *Recorder) ReportResult(ctx context.Context, result lease.Result) error {
	recorder.mu.Lock()
	copied := result
	if result.ExitCode != nil {
		code := *result.ExitCode
		copied.ExitCode = &code
	}
	recorder.last = &copied
	recorder.mu.Unlock()
	return recorder.next.ReportResult(ctx, result)
}

// LastResult returns the most recently finished lease, if any.
func (recorder *Recorder) LastResult() (lease.Result, bool) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.last == nil {
		return lease.Result{}, false
	}
	return *recorder.last, true
}
