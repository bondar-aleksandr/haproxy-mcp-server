package haproxy

import (
	"errors"
	"strings"
)

// opStates maps the numeric srv_op_state column of "show servers state" to a name.
var opStates = map[string]string{"0": "STOPPED", "1": "STARTING", "2": "RUNNING", "3": "STOPPING"}

// ParseServersState parses the output of "show servers state":
//
//	1
//	# be_id be_name srv_id srv_name srv_addr srv_op_state ...
//	5 folio_BE 1 folio-ingress 10.10.100.0 2 ...
//
// i.e. a format version line, a "#"-prefixed header line, then one whitespace-separated line per
// server. It returns one map per server, keyed by the header names; a readable "srv_op_state_name"
// is added next to the numeric srv_op_state.
//
// Output without header is HAProxy's error message (e.g. "Can't find backend."): it is returned as an
// error.
func ParseServersState(output string) ([]map[string]string, error) {
	if msg := strings.TrimSpace(output); msg != "" && !strings.Contains(msg, "#") {
		return nil, errors.New(msg)
	}
	var headers []string
	rows := []map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			headers = strings.Fields(strings.TrimPrefix(line, "#"))
			continue
		}
		if headers == nil {
			continue // format version line
		}
		fields := strings.Fields(line)
		row := make(map[string]string, len(headers)+1)
		for i := 0; i < len(headers) && i < len(fields); i++ {
			row[headers[i]] = fields[i]
		}
		if name, ok := opStates[row["srv_op_state"]]; ok {
			row["srv_op_state_name"] = name
		}
		rows = append(rows, row)
	}
	return rows, nil
}

