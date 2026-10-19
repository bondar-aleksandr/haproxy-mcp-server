package haproxy

import "testing"

// Output of HAProxy 3.0 for "show servers state web_BE": a format version line, a "#" header, one line per server.
const serversState = `1
# be_id be_name srv_id srv_name srv_addr srv_op_state srv_admin_state srv_uweight srv_iweight srv_time_since_last_change srv_check_status srv_check_result srv_check_health srv_check_state srv_agent_state bk_f_forced_id srv_f_forced_id srv_fqdn srv_port srvrecord srv_use_ssl srv_check_port srv_check_addr srv_agent_addr srv_agent_port
5 web_BE 1 app1 10.10.100.1 2 0 1 1 217 6 3 4 6 0 0 0 - 80 - 0 0 - - 0
5 web_BE 2 app2 10.10.100.2 0 0 1 1 30 6 3 0 6 0 0 0 - 8080 - 0 0 - - 0
`

func TestParseServersState(t *testing.T) {
	rows, err := ParseServersState(serversState)
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %v", rows)
	}
	if rows[0]["srv_name"] != "app1" || rows[0]["srv_addr"] != "10.10.100.1" || rows[0]["srv_port"] != "80" ||
		rows[0]["srv_op_state_name"] != "RUNNING" || rows[1]["srv_op_state_name"] != "STOPPED" {
		t.Errorf("unexpected rows: %v", rows)
	}
	if _, err := ParseServersState("Can't find backend.\n"); err == nil || err.Error() != "Can't find backend." {
		t.Errorf("HAProxy's message should be returned as an error, got %v", err)
	}
	if rows, err := ParseServersState("1\n# be_id be_name\n"); err != nil || len(rows) != 0 {
		t.Errorf("header without servers should give no rows, got %v, %v", rows, err)
	}
}
