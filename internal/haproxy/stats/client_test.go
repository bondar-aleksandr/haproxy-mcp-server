package stats

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Trimmed copy of the real "/stats;json" output of HAProxy (array of rows, each an array of fields).
const sampleStatsJSON = `[
 [
  {"objType":"Frontend","proxyId":2,"id":0,"field":{"pos":0,"name":"pxname"},"processNum":1,"tags":{},"value":{"type":"str","value":"stats"}},
  {"objType":"Frontend","proxyId":2,"id":0,"field":{"pos":1,"name":"svname"},"processNum":1,"tags":{},"value":{"type":"str","value":"FRONTEND"}},
  {"objType":"Frontend","proxyId":2,"id":0,"field":{"pos":7,"name":"stot"},"processNum":1,"tags":{},"value":{"type":"u64","value":50}},
  {"objType":"Frontend","proxyId":2,"id":0,"field":{"pos":17,"name":"status"},"processNum":1,"tags":{},"value":{"type":"str","value":"OPEN"}},
  {"objType":"Frontend","proxyId":2,"id":0,"field":{"pos":32,"name":"type"},"processNum":1,"tags":{},"value":{"type":"u32","value":0}}
 ],
 [
  {"objType":"Backend","proxyId":5,"id":0,"field":{"pos":0,"name":"pxname"},"processNum":1,"tags":{},"value":{"type":"str","value":"folio_BE"}},
  {"objType":"Backend","proxyId":5,"id":0,"field":{"pos":1,"name":"svname"},"processNum":1,"tags":{},"value":{"type":"str","value":"BACKEND"}},
  {"objType":"Backend","proxyId":5,"id":0,"field":{"pos":17,"name":"status"},"processNum":1,"tags":{},"value":{"type":"str","value":"UP"}},
  {"objType":"Backend","proxyId":5,"id":0,"field":{"pos":18,"name":"weight"},"processNum":1,"tags":{},"value":{"type":"u32","value":1}},
  {"objType":"Backend","proxyId":5,"id":0,"field":{"pos":32,"name":"type"},"processNum":1,"tags":{},"value":{"type":"u32","value":1}}
 ],
 [
  {"objType":"Server","proxyId":5,"id":1,"field":{"pos":0,"name":"pxname"},"processNum":1,"tags":{},"value":{"type":"str","value":"folio_BE"}},
  {"objType":"Server","proxyId":5,"id":1,"field":{"pos":1,"name":"svname"},"processNum":1,"tags":{},"value":{"type":"str","value":"folio-ingress"}},
  {"objType":"Server","proxyId":5,"id":1,"field":{"pos":17,"name":"status"},"processNum":1,"tags":{},"value":{"type":"str","value":"UP"}},
  {"objType":"Server","proxyId":5,"id":1,"field":{"pos":18,"name":"weight"},"processNum":1,"tags":{},"value":{"type":"u32","value":1}},
  {"objType":"Server","proxyId":5,"id":1,"field":{"pos":73,"name":"addr"},"processNum":1,"tags":{},"value":{"type":"str","value":"10.10.100.0:80"}}
 ]
]`

func TestHAProxyStatsUnmarshal(t *testing.T) {
	var s HAProxyStats
	if err := s.UnmarshalJSON([]byte(sampleStatsJSON)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.Stats) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.Stats))
	}
	fe, be, srv := s.Stats[0], s.Stats[1], s.Stats[2]
	if fe.PxName != "stats" || fe.SvName != "FRONTEND" || fe.Type != 0 || fe.Status != "OPEN" || fe.Fields["stot"] != "50" {
		t.Errorf("unexpected frontend: %+v", fe)
	}
	if be.Type != 1 || be.Weight != 1 || be.Status != "UP" {
		t.Errorf("unexpected backend: %+v", be)
	}
	// "type" field is absent for the server row: derived from objType
	if srv.Type != 2 || srv.SvName != "folio-ingress" || srv.Fields["addr"] != "10.10.100.0:80" {
		t.Errorf("unexpected server: %+v", srv)
	}
}

func TestHAProxyStatsUnmarshalLegacyShape(t *testing.T) {
	var s HAProxyStats
	if err := s.UnmarshalJSON([]byte(`{"stats":[{"pxname":"a","svname":"b","type":2,"status":"UP","weight":3}]}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(s.Stats) != 1 || s.Stats[0].PxName != "a" || s.Stats[0].Weight != 3 {
		t.Errorf("unexpected stats: %+v", s.Stats)
	}
}

func TestGetStats(t *testing.T) {
	for _, base := range []string{"/stats", "/stats/", "/stats;json"} {
		t.Run(base, func(t *testing.T) {
			var gotURI string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotURI = r.RequestURI
				_, _ = w.Write([]byte(sampleStatsJSON))
			}))
			defer srv.Close()

			c, err := NewStatsClient(srv.URL + base)
			if err != nil {
				t.Fatal(err)
			}
			st, err := c.GetStats()
			if err != nil {
				t.Fatalf("GetStats: %v", err)
			}
			if gotURI != "/stats;json" {
				t.Errorf("requested %q, want /stats;json", gotURI)
			}
			if len(st.Stats) != 3 || len(c.GetServers(st)) != 1 || len(c.GetBackends(st)) != 1 || len(c.GetFrontends(st)) != 1 {
				t.Errorf("unexpected parsed stats: %+v", st.Stats)
			}
		})
	}
}

func TestBuildURLRoot(t *testing.T) {
	c := &StatsClient{StatsURL: "http://h:8404/"}
	if got := c.buildURL(";json"); got != "http://h:8404/;json" {
		t.Errorf("got %q", got)
	}
}
