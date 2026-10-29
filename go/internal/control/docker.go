// DEMO-ONLY chaos through the Docker Engine API: plain HTTP over the unix socket, API version pinned.
//
// What each action means for the demo:
//   - kill    = process crash (SIGKILL, no graceful shutdown)
//   - stop    = clean shutdown (SIGTERM, 10 s grace)
//   - pause   = the process is frozen: TCP connections stay open but nothing answers. For Core or SQL Server this
//     is a HANG, which exercises the Activity StartToClose (60 s) and the 30 s SQL timeout realistically.
//   - restart, start, unpause
//
// Only services named in CHAOS_ALLOWLIST can be touched; anything else is refused (403). Arbitrary Docker access
// is never exposed.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// DockerAPIVersion is pinned: every endpoint used here exists in 1.47 (Docker Engine 27+).
const DockerAPIVersion = "v1.47"

// Container is what the Control Center shows per service.
type Container struct {
	Service   string    `json:"service"`
	ID        string    `json:"id"`
	State     string    `json:"state"`  // running | exited | paused | created | restarting
	Health    string    `json:"health"` // healthy | unhealthy | starting | "" (no healthcheck)
	Status    string    `json:"status"` // Docker's human text, e.g. "Up 3 minutes"
	StartedAt time.Time `json:"startedAt"`
	Allowed   bool      `json:"allowed"`
}

// ContainerAPI is the Docker surface control-service needs (a fake in tests).
type ContainerAPI interface {
	List(ctx context.Context) ([]Container, error)
	Act(ctx context.Context, service, action string) error
}

var dockerActions = []string{"start", "stop", "kill", "restart", "pause", "unpause"}

// ErrNotAllowed means the service is not in CHAOS_ALLOWLIST.
var ErrNotAllowed = errors.New("service is not in CHAOS_ALLOWLIST")

// Docker talks to the Docker Engine over /var/run/docker.sock, scoped to one compose project.
type Docker struct {
	project   string
	allowlist []string
	http      *http.Client
}

// NewDocker builds a client for the compose project.
func NewDocker(socket, project string, allowlist []string) *Docker {
	return &Docker{project: project, allowlist: allowlist, http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// Allowed reports whether a compose service may be acted on.
func (d *Docker) Allowed(service string) bool { return slices.Contains(d.allowlist, service) }

func (d *Docker) call(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+DockerAPIVersion+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 304 = already started/stopped: the desired state already holds.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("docker %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

// List returns every container of the compose project (including stopped ones), one per service.
func (d *Docker) List(ctx context.Context) ([]Container, error) {
	filter, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + d.project}})
	var raw []struct {
		ID     string            `json:"Id"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
	}
	if err := d.call(ctx, http.MethodGet, "/containers/json?all=1&filters="+url.QueryEscape(string(filter)), &raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, c := range raw {
		svc := c.Labels["com.docker.compose.service"]
		ct := Container{Service: svc, ID: c.ID[:12], State: c.State, Status: c.Status, Allowed: d.Allowed(svc)}
		var insp struct {
			State struct {
				StartedAt time.Time `json:"StartedAt"`
				Health    *struct {
					Status string `json:"Status"`
				} `json:"Health"`
			} `json:"State"`
		}
		if err := d.call(ctx, http.MethodGet, "/containers/"+c.ID+"/json", &insp); err == nil {
			ct.StartedAt = insp.State.StartedAt
			if insp.State.Health != nil {
				ct.Health = insp.State.Health.Status
			}
		}
		out = append(out, ct)
	}
	return out, nil
}

// Act performs one chaos action on an allowlisted service's container.
func (d *Docker) Act(ctx context.Context, service, action string) error {
	if !d.Allowed(service) {
		return ErrNotAllowed
	}
	if !slices.Contains(dockerActions, action) {
		return fmt.Errorf("unknown action %q", action)
	}
	id, err := d.containerID(ctx, service)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/containers/%s/%s", id, action)
	switch action {
	case "stop", "restart":
		path += "?t=10"
	case "kill":
		path += "?signal=SIGKILL"
	}
	return d.call(ctx, http.MethodPost, path, nil)
}

func (d *Docker) containerID(ctx context.Context, service string) (string, error) {
	cs, err := d.List(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range cs {
		if c.Service == service {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no container for service %q (for worker-v2/v3 run `make up` or `make versioning-demo`)", service)
}

func (s *Service) containerAction(w http.ResponseWriter, r *http.Request) {
	service, action := r.PathValue("service"), r.PathValue("action")
	ctx, cancel := timeout(r.Context(), 30*time.Second)
	defer cancel()
	err := s.Docker.Act(ctx, service, action)
	switch {
	case errors.Is(err, ErrNotAllowed):
		writeError(w, http.StatusForbidden, fmt.Sprintf("%s: %v", service, err))
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.Log.Info("DEMO-ONLY chaos action", "service", service, "action", action)
		writeJSON(w, http.StatusOK, map[string]string{"service": service, "action": action, "result": "ok"})
	}
}
