package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// Engine is the part of the Docker Engine the UI uses. The SDK stays behind it so the poller and
// the models can be tested with a fake.
type Engine interface {
	Info(ctx context.Context) (Info, error)
	Containers(ctx context.Context) ([]ContainerInfo, error)
	// StreamStats calls onFrame for every stats frame (about one per second) until the stream ends or ctx is cancelled.
	StreamStats(ctx context.Context, id string, onFrame func(Stats)) error
	StartedAt(ctx context.Context, id string) (time.Time, error)
	// Action is "start", "stop" or "restart"; nothing else is accepted.
	Action(ctx context.Context, id, verb string) error
	// Logs calls onLine per line until the stream ends or ctx is cancelled; stream is 1 (stdout) or 2 (stderr).
	Logs(ctx context.Context, id string, tail int, follow bool, onLine func(stream byte, line string)) error
	Images(ctx context.Context) ([]ImageInfo, error)
	Volumes(ctx context.Context) ([]VolumeInfo, error)
}

const maxLineBytes = 1 << 20

type Port struct {
	Public, Private uint16
}

// ContainerInfo is the list entry of a container; it has no environment field.
type ContainerInfo struct {
	ID     string
	Names  []string
	Image  string
	State  string
	Status string
	Ports  []Port
	Labels map[string]string
}

type Info struct {
	NCPU          int
	MemTotal      int64
	ServerVersion string
}

// Stats is one flattened sample; the Pre fields are the previous sample from the same reply.
type Stats struct {
	CPUTotal, PreCPUTotal           uint64
	CPUSystem, PreCPUSystem         uint64
	OnlineCPUs                      int
	MemUsage, MemInactive, MemLimit uint64
}

type ImageInfo struct {
	ID       string
	RepoTags []string
	Size     int64
	Created  int64
}

type VolumeInfo struct{ Name, Driver, Mountpoint string }

// sdkEngine implements Engine with the official Docker SDK.
type sdkEngine struct{ cli *client.Client }

// newEngine uses DOCKER_HOST and friends; without DOCKER_HOST it also tries the Docker Desktop
// socket in the home directory when /var/run/docker.sock is absent. API versions are negotiated by the SDK.
func newEngine() (Engine, error) {
	opts := []client.Opt{client.FromEnv}
	if os.Getenv("DOCKER_HOST") == "" {
		if p := desktopSocket(); p != "" {
			opts = append(opts, client.WithHost("unix://"+p))
		}
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, err
	}
	return &sdkEngine{cli}, nil
}

func desktopSocket() string {
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".docker", "run", "docker.sock")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func (e *sdkEngine) Info(ctx context.Context) (Info, error) {
	r, err := e.cli.Info(ctx, client.InfoOptions{})
	return Info{NCPU: r.Info.NCPU, MemTotal: r.Info.MemTotal, ServerVersion: r.Info.ServerVersion}, err
}

func (e *sdkEngine) Containers(ctx context.Context) ([]ContainerInfo, error) {
	r, err := e.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, len(r.Items))
	for i, c := range r.Items {
		ports := make([]Port, len(c.Ports))
		for j, p := range c.Ports {
			ports[j] = Port{Public: p.PublicPort, Private: p.PrivatePort}
		}
		out[i] = ContainerInfo{ID: c.ID, Names: c.Names, Image: c.Image, State: string(c.State), Status: c.Status, Ports: ports, Labels: c.Labels}
	}
	return out, nil
}

func (e *sdkEngine) StreamStats(ctx context.Context, id string, onFrame func(Stats)) error {
	r, err := e.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return err
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	var doc statsDoc
	for {
		doc = statsDoc{}
		if err := dec.Decode(&doc); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		onFrame(doc.flat())
	}
}

func (d *statsDoc) flat() Stats {
	inactive := d.Mem.Stats.InactiveFile
	if inactive == 0 {
		inactive = d.Mem.Stats.TotalInactiveFile
	}
	return Stats{
		CPUTotal: d.CPU.Usage.Total, PreCPUTotal: d.PreCPU.Usage.Total,
		CPUSystem: d.CPU.System, PreCPUSystem: d.PreCPU.System, OnlineCPUs: d.CPU.Online,
		MemUsage: d.Mem.Usage, MemInactive: inactive, MemLimit: d.Mem.Limit,
	}
}

// statsDoc names only the two cache counters instead of decoding the whole memory stats map, which
// would allocate dozens of entries per frame.
type statsDoc struct {
	CPU    cpuSample `json:"cpu_stats"`
	PreCPU cpuSample `json:"precpu_stats"`
	Mem    struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile      uint64 `json:"inactive_file"`
			TotalInactiveFile uint64 `json:"total_inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
}

type cpuSample struct {
	Usage struct {
		Total uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	System uint64 `json:"system_cpu_usage"`
	Online int    `json:"online_cpus"`
}

// StartedAt: the inspect reply also carries the environment; it is fetched but only State.StartedAt is kept, nothing is shown or stored.
func (e *sdkEngine) StartedAt(ctx context.Context, id string) (time.Time, error) {
	r, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || r.Container.State == nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, r.Container.State.StartedAt)
}

func (e *sdkEngine) tty(ctx context.Context, id string) bool {
	r, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	return err == nil && r.Container.Config != nil && r.Container.Config.Tty
}

func (e *sdkEngine) Action(ctx context.Context, id, verb string) (err error) {
	switch verb {
	case "start":
		_, err = e.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	case "stop":
		_, err = e.cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	case "restart":
		_, err = e.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	default:
		err = fmt.Errorf("unsupported action %q", verb)
	}
	return err
}

func (e *sdkEngine) Logs(ctx context.Context, id string, tail int, follow bool, onLine func(byte, string)) error {
	r, err := e.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow, Tail: fmt.Sprint(tail)})
	if err != nil {
		return err
	}
	defer r.Close()
	if e.tty(ctx, id) {
		return scanLines(r, onLine)
	}
	out, errw := &lineWriter{stream: 1, onLine: onLine}, &lineWriter{stream: 2, onLine: onLine}
	_, err = stdcopy.StdCopy(out, errw, r)
	out.flush()
	errw.flush()
	return err
}

// lineWriter turns the demultiplexed byte stream into lines.
type lineWriter struct {
	stream byte
	onLine func(byte, string)
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			// A line without a newline must not grow without bound.
			if len(w.buf) > maxLineBytes {
				w.flush()
			}
			return len(p), nil
		}
		w.onLine(w.stream, string(bytes.TrimSuffix(w.buf[:i], []byte("\r"))))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.onLine(w.stream, string(w.buf))
		w.buf = nil
	}
}

func scanLines(r io.Reader, onLine func(byte, string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		onLine(1, string(bytes.TrimSuffix(sc.Bytes(), []byte("\r"))))
	}
	return sc.Err()
}

func (e *sdkEngine) Images(ctx context.Context) ([]ImageInfo, error) {
	r, err := e.cli.ImageList(ctx, client.ImageListOptions{})
	out := make([]ImageInfo, len(r.Items))
	for i, im := range r.Items {
		out[i] = ImageInfo{ID: im.ID, RepoTags: im.RepoTags, Size: im.Size, Created: im.Created}
	}
	return out, err
}

func (e *sdkEngine) Volumes(ctx context.Context) ([]VolumeInfo, error) {
	r, err := e.cli.VolumeList(ctx, client.VolumeListOptions{})
	out := make([]VolumeInfo, len(r.Items))
	for i, v := range r.Items {
		out[i] = VolumeInfo{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint}
	}
	return out, err
}
