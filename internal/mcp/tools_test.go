package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy"
	hatesting "github.com/tuannvm/haproxy-mcp-server/internal/haproxy/testing"
)

// registry builds a registry of single-client instances, named after the map keys.
func registry(t *testing.T, clients map[string]*haproxy.HAProxyClient) *haproxy.Registry {
	t.Helper()
	var instances []*haproxy.Instance
	for name, c := range clients {
		inst := &haproxy.Instance{Name: name, RuntimeURL: c.RuntimeURL, StatsURL: c.StatsURL}
		// Mock clients carry no URLs: any placeholder marks the API as configured.
		if c.HasRuntime() && inst.RuntimeURL == "" {
			inst.RuntimeURL = "mock://runtime"
		}
		if c.HasStats() && inst.StatsURL == "" {
			inst.StatsURL = "mock://stats"
		}
		inst.SetClient(c)
		instances = append(instances, inst)
	}
	reg, err := haproxy.NewRegistry(instances...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func toolNames(t *testing.T, reg *haproxy.Registry) []string {
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, reg)
	var names []string
	for n := range s.ListTools() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStatsOnlyToolSet(t *testing.T) {
	reg := registry(t, map[string]*haproxy.HAProxyClient{"a": {StatsClient: hatesting.NewMockStatsClient()}})
	got := toolNames(t, reg)
	want := []string{"list_backends", "list_frontends", "list_services", "show_stat"}
	if !equal(got, want) {
		t.Errorf("stats-only tools = %v, want %v", got, want)
	}
}

func TestRuntimeToolSet(t *testing.T) {
	reg := registry(t, map[string]*haproxy.HAProxyClient{"a": {RuntimeClient: hatesting.NewMockRuntimeClient()}})
	got := toolNames(t, reg)
	want := []string{"clear_counters_all", "debug_counters", "list_backends", "list_frontends", "list_services", "show_info", "show_stat"}
	if !equal(got, want) {
		t.Errorf("runtime tools = %v, want %v", got, want)
	}
}

func TestToolAnnotations(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, registry(t, map[string]*haproxy.HAProxyClient{"a": {RuntimeClient: hatesting.NewMockRuntimeClient()}}))
	readOnlyTools := map[string]bool{
		"show_stat": true, "list_frontends": true, "list_services": true, "list_backends": true,
		"show_info": true, "debug_counters": true,
	}
	for name, st := range s.ListTools() {
		a := st.Tool.Annotations
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: openWorldHint should be false", name)
		}
		if readOnlyTools[name] {
			if a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
				t.Errorf("%s: should be read-only and non-destructive: %+v", name, a)
			}
		} else if a.ReadOnlyHint != nil && *a.ReadOnlyHint {
			t.Errorf("%s: must not be read-only", name)
		}
	}
}

// twoInstances: "prod" has the Runtime API and the stats page, "edge" only the stats page.
func twoInstances(t *testing.T) *server.MCPServer {
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, registry(t, map[string]*haproxy.HAProxyClient{
		"prod": {
			RuntimeClient: hatesting.NewMockRuntimeClient(), RuntimeURL: "tcp://10.0.0.1:9999",
			StatsClient: hatesting.NewMockStatsClient(), StatsURL: "http://admin:secret@10.0.0.1:8404/stats",
		},
		"edge": {StatsClient: hatesting.NewMockStatsClient(), StatsURL: "http://10.0.1.1:8404/stats"},
	}))
	return s
}

func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	st, ok := s.ListTools()[name]
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestInstanceParameter(t *testing.T) {
	s := twoInstances(t)
	// Every tool gets an "instance" argument, required with several instances, restricted to known names.
	for name, st := range s.ListTools() {
		prop, ok := st.Tool.InputSchema.Properties[instanceParam].(map[string]any)
		if !ok {
			t.Errorf("%s: no %q property", name, instanceParam)
			continue
		}
		if enum := prop["enum"]; len(enum.([]string)) != 2 {
			t.Errorf("%s: unexpected enum %v", name, enum)
		}
		required := false
		for _, r := range st.Tool.InputSchema.Required {
			required = required || r == instanceParam
		}
		if !required {
			t.Errorf("%s: %q should be required with several instances", name, instanceParam)
		}
	}

	// Single instance: optional.
	single := server.NewMCPServer("test", "0")
	RegisterTools(single, registry(t, map[string]*haproxy.HAProxyClient{"a": {StatsClient: hatesting.NewMockStatsClient()}}))
	for _, r := range single.ListTools()["show_stat"].Tool.InputSchema.Required {
		if r == instanceParam {
			t.Error("instance must be optional with a single instance")
		}
	}
	if res := callTool(t, single, "show_stat", nil); res.IsError {
		t.Errorf("show_stat without instance should work with one instance: %s", resultText(res))
	}
}

func TestInstanceRouting(t *testing.T) {
	s := twoInstances(t)

	res := callTool(t, s, "show_stat", nil)
	if !res.IsError || !strings.Contains(resultText(res), "edge, prod") {
		t.Errorf("missing instance should fail listing the names: %q", resultText(res))
	}
	res = callTool(t, s, "show_stat", map[string]any{"instance": "nope"})
	if !res.IsError || !strings.Contains(resultText(res), "unknown HAProxy instance") {
		t.Errorf("unknown instance should fail: %q", resultText(res))
	}
	if res := callTool(t, s, "show_stat", map[string]any{"instance": "edge"}); res.IsError {
		t.Errorf("show_stat on stats-only instance should work: %s", resultText(res))
	}
	// Runtime-only tool: fine on prod, a clear error on the stats-only instance.
	res = callTool(t, s, "show_info", map[string]any{"instance": "edge"})
	if !res.IsError || !strings.Contains(resultText(res), "no Runtime API") {
		t.Errorf("show_info on stats-only instance should explain why: %q", resultText(res))
	}
	if res := callTool(t, s, "show_info", map[string]any{"instance": "prod"}); res.IsError {
		t.Errorf("show_info on prod should work: %s", resultText(res))
	}
}

func TestInstancesResource(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, registry(t, map[string]*haproxy.HAProxyClient{
		"prod": {
			RuntimeClient: hatesting.NewMockRuntimeClient(), RuntimeURL: "tcp://10.0.0.1:9999",
			StatsClient: hatesting.NewMockStatsClient(), StatsURL: "http://admin:secret@10.0.0.1:8404/stats",
		},
		"edge": {StatsClient: hatesting.NewMockStatsClient(), StatsURL: "http://10.0.1.1:8404/stats"},
	}))

	res, ok := s.ListResources()[instancesResourceURI]
	if !ok {
		t.Fatalf("resource %s not registered", instancesResourceURI)
	}
	req := mcp.ReadResourceRequest{}
	req.Params.URI = instancesResourceURI
	contents, err := res.Handler(context.Background(), req)
	if err != nil || len(contents) != 1 {
		t.Fatalf("read: %v %v", contents, err)
	}
	text := contents[0].(mcp.TextResourceContents).Text
	if strings.Contains(text, "secret") {
		t.Errorf("credentials leaked in resource: %s", text)
	}
	var doc struct {
		Instances []struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
			Addresses    []string `json:"addresses"`
		} `json:"instances"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Instances) != 2 || doc.Instances[0].Name != "edge" || doc.Instances[1].Name != "prod" {
		t.Fatalf("unexpected instances: %s", text)
	}
	if got := strings.Join(doc.Instances[0].Capabilities, ","); got != "stats" || strings.Join(doc.Instances[0].Addresses, ",") != "10.0.1.1" {
		t.Errorf("edge: capabilities %q, addresses %v", got, doc.Instances[0].Addresses)
	}
	// The same host serves both APIs: it is listed once, without ports, paths or credentials.
	if got := strings.Join(doc.Instances[1].Capabilities, ","); got != "runtime,stats" || strings.Join(doc.Instances[1].Addresses, ",") != "10.0.0.1" {
		t.Errorf("prod: capabilities %q, addresses %v", got, doc.Instances[1].Addresses)
	}
	for _, key := range []string{`"runtime"`, `"stats"`, `"url"`} { // the endpoint objects are gone
		if strings.Contains(text, key+":") {
			t.Errorf("unexpected key %s in the resource: %s", key, text)
		}
	}
}

func TestUnavailableInstance(t *testing.T) {
	up := &haproxy.Instance{Name: "up", StatsURL: "http://u:pw@h/stats"}
	up.SetClient(&haproxy.HAProxyClient{StatsClient: hatesting.NewMockStatsClient()})
	down := &haproxy.Instance{
		Name: "down", StatsURL: "http://u:pw@h2/stats", RuntimeURL: "tcp://h2:9999",
		Connect: func() (*haproxy.HAProxyClient, error) { return nil, errors.New("dial h2 with pw failed") },
	}
	_ = down.TryConnect()
	reg, err := haproxy.NewRegistry(up, down)
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, reg)

	// Tools are registered from the configuration, even though "down" is the only one with a runtime API.
	if _, ok := s.ListTools()["show_info"]; !ok {
		t.Error("runtime tools must be registered for a configured but unavailable runtime API")
	}
	res := callTool(t, s, "show_stat", map[string]any{"instance": "down"})
	text := resultText(res)
	if !res.IsError || !strings.Contains(text, "unavailable") || strings.Contains(text, "pw") {
		t.Errorf("expected an 'unavailable' error without secrets, got %q", text)
	}
	if res := callTool(t, s, "show_stat", map[string]any{"instance": "up"}); res.IsError {
		t.Errorf("available instance should work: %s", resultText(res))
	}

	// The resource reports the status and the reason, and updates when the instance recovers.
	read := func() []haproxy.InstanceInfo {
		req := mcp.ReadResourceRequest{}
		req.Params.URI = instancesResourceURI
		contents, err := s.ListResources()[instancesResourceURI].Handler(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ Instances []haproxy.InstanceInfo }
		if err := json.Unmarshal([]byte(contents[0].(mcp.TextResourceContents).Text), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Instances
	}
	infos := read()
	if infos[0].Name != "down" || infos[0].Status != "unavailable" || !strings.Contains(infos[0].Error, "failed") ||
		strings.Contains(infos[0].Error, "pw") || len(infos[0].Capabilities) != 2 {
		t.Errorf("unexpected unavailable entry: %+v", infos[0])
	}
	if infos[1].Name != "up" || infos[1].Status != "available" {
		t.Errorf("unexpected available entry: %+v", infos[1])
	}

	down.SetClient(&haproxy.HAProxyClient{RuntimeClient: hatesting.NewMockRuntimeClient()})
	if got := read()[0]; got.Status != "available" || got.Error != "" {
		t.Errorf("instance should be available after recovery: %+v", got)
	}
	if res := callTool(t, s, "show_info", map[string]any{"instance": "down"}); res.IsError {
		t.Errorf("recovered instance should serve tools: %s", resultText(res))
	}
}

func TestOverviewToolArguments(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	RegisterTools(s, registry(t, map[string]*haproxy.HAProxyClient{"a": {StatsClient: hatesting.NewMockStatsClient()}}))
	tools := s.ListTools()

	enum := func(tool string) []string {
		prop, ok := tools[tool].Tool.InputSchema.Properties["detail"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no detail argument", tool)
		}
		return prop["enum"].([]string)
	}
	if got := strings.Join(enum("list_backends"), ","); got != "terse,standard,full" {
		t.Errorf("list_backends detail = %s", got)
	}
	if got := strings.Join(enum("list_services"), ","); got != "terse,standard" {
		t.Errorf("list_services detail = %s", got)
	}
	for _, tool := range []string{"list_backends", "list_frontends", "list_services"} {
		if _, ok := tools[tool].Tool.InputSchema.Properties["name"]; !ok {
			t.Errorf("%s has no name argument", tool)
		}
	}
	if _, ok := tools["list_backends"].Tool.InputSchema.Properties["server"]; !ok {
		t.Error("list_backends has no server argument")
	}

	// The mock stats have one backend, "backend1", with a server "server1".
	res := callTool(t, s, "list_backends", nil)
	var terse struct{ Backends []struct{ Name string } }
	if err := json.Unmarshal([]byte(resultText(res)), &terse); err != nil || len(terse.Backends) != 1 || terse.Backends[0].Name != "backend1" {
		t.Errorf("terse list_backends: %q (%v)", resultText(res), err)
	}
	if res := callTool(t, s, "list_backends", map[string]any{"detail": "verbose"}); !res.IsError || !strings.Contains(resultText(res), "invalid detail") {
		t.Errorf("an invalid detail must be rejected: %q", resultText(res))
	}
	if res := callTool(t, s, "list_backends", map[string]any{"name": "nope"}); !res.IsError || !strings.Contains(resultText(res), "backend1") {
		t.Errorf("an unknown name must list the known ones: %q", resultText(res))
	}
	if res := callTool(t, s, "list_backends", map[string]any{"name": "backend1", "server": "server1", "detail": "full"}); res.IsError {
		t.Errorf("name+server+full should work: %s", resultText(res))
	}
}
