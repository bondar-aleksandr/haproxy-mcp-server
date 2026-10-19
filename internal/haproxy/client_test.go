package haproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const showStatJSON = `[[
 {"objType":"Server","field":{"name":"pxname"},"value":{"type":"str","value":"be"}},
 {"objType":"Server","field":{"name":"svname"},"value":{"type":"str","value":"srv1"}},
 {"objType":"Server","field":{"name":"status"},"value":{"type":"str","value":"UP"}},
 {"objType":"Server","field":{"name":"scur"},"value":{"type":"u32","value":7}}
]]`

func TestShowStatStatsOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(showStatJSON))
	}))
	defer srv.Close()

	c, err := NewHAProxyClient("", srv.URL+"/stats")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.ShowStat("srv")
	if err != nil {
		t.Fatalf("ShowStat: %v", err)
	}
	if len(rows) != 1 || rows[0]["svname"] != "srv1" || rows[0]["scur"] != "7" || rows[0]["status"] != "UP" {
		t.Errorf("unexpected rows: %+v", rows)
	}
	if rows, _ := c.ShowStat("nomatch"); len(rows) != 0 {
		t.Errorf("filter not applied: %+v", rows)
	}
}

func TestShowStatReportsStatsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	c, err := NewHAProxyClient("", srv.URL+"/stats")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ShowStat("")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected the underlying stats error (404), got %v", err)
	}
}

const backendsJSON = `[
 [{"objType":"Backend","field":{"name":"pxname"},"value":{"type":"str","value":"be"}},
  {"objType":"Backend","field":{"name":"svname"},"value":{"type":"str","value":"BACKEND"}},
  {"objType":"Backend","field":{"name":"scur"},"value":{"type":"u32","value":3}},
  {"objType":"Backend","field":{"name":"status"},"value":{"type":"str","value":"UP"}},
  {"objType":"Backend","field":{"name":"type"},"value":{"type":"u32","value":1}}],
 [{"objType":"Server","field":{"name":"pxname"},"value":{"type":"str","value":"be"}},
  {"objType":"Server","field":{"name":"svname"},"value":{"type":"str","value":"srv1"}},
  {"objType":"Server","field":{"name":"status"},"value":{"type":"str","value":"DOWN"}},
  {"objType":"Server","field":{"name":"addr"},"value":{"type":"str","value":"10.0.0.1:80"}},
  {"objType":"Server","field":{"name":"type"},"value":{"type":"u32","value":2}}],
 [{"objType":"Frontend","field":{"name":"pxname"},"value":{"type":"str","value":"fe"}},
  {"objType":"Frontend","field":{"name":"svname"},"value":{"type":"str","value":"FRONTEND"}},
  {"objType":"Frontend","field":{"name":"type"},"value":{"type":"u32","value":0}}]
]`

// csvRuntime is a runtime client answering "show stat" with a canned CSV.
type csvRuntime struct {
	RuntimeClient // unused methods panic if called
	commands      []string
}

func (r *csvRuntime) ExecuteRuntimeCommand(cmd string) (string, error) {
	r.commands = append(r.commands, cmd)
	return "# pxname,svname,scur,smax,slim,stot,bin,bout,status,type,mode,\n" +
		"web_FE,FRONTEND,2,5,1000,12,300,400,OPEN,0,http,\n" +
		"web_FE,srv1,0,0,,0,0,0,UP,2,http,\n" +
		"web_BE,BACKEND,0,0,100,0,0,0,UP,1,http,\n" +
		"admin_FE,FRONTEND,0,0,1000,0,0,0,STOP,0,tcp,\n", nil
}

func TestShowStatFromRuntime(t *testing.T) {
	rt := &csvRuntime{}
	c := &HAProxyClient{RuntimeClient: rt}

	// The filter is applied by the client: "show stat" itself takes no names.
	rows, err := c.ShowStat("web_BE")
	if err != nil || len(rows) != 1 || rows[0]["svname"] != "BACKEND" {
		t.Errorf("ShowStat(web_BE) = %v, %v", rows, err)
	}
	if rows, err := c.ShowStat(""); err != nil || len(rows) != 4 || rows[0]["type"] != "0" {
		t.Errorf("ShowStat() = %v, %v", rows, err)
	}
	for _, cmd := range rt.commands {
		if cmd != "show stat" {
			t.Errorf("unexpected command %q", cmd)
		}
	}
}
