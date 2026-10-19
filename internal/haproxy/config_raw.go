package haproxy

import (
	"fmt"
	"strings"
	"time"

	"github.com/tuannvm/haproxy-mcp-server/internal/haproxy/hacfg"
)

// ConfigDump is the raw configuration of an instance.
type ConfigDump struct {
	Text     string             // the configuration, or the sections matching the filter, with credentials masked
	File     string             // the configured config_file
	Modified time.Time          // when the file was last modified
	Lines    int                // lines of the whole configuration
	Sections []hacfg.SectionRef // the sections returned, with where they are; only when filtering
	Redacted int                // values masked in Text
	Warnings []string
}

// ShowConfig returns the instance's haproxy.cfg as written, with the values that look like
// credentials masked (best effort, see hacfg.Redact). With a filter, only the sections whose header
// ("frontend web_FE", "backend app_BE", "global", "defaults") contains it are returned. Only the
// configured config_file can be read.
func (i *Instance) ShowConfig(filter string) (*ConfigDump, error) {
	if i.ConfigFile == "" {
		return nil, fmt.Errorf("instance %q has no config_file configured", i.Name)
	}
	src, err := hacfg.ReadSource(i.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read the config file: %w", err)
	}
	dump := &ConfigDump{
		File:     i.ConfigFile,
		Modified: src.Modified,
		Lines:    strings.Count(src.Text, "\n"),
	}

	text, matched, known := hacfg.Filter(src.Text, filter)
	if filter != "" && len(matched) == 0 {
		return nil, fmt.Errorf("no section matches %q, the sections are: %s", filter, strings.Join(known, ", "))
	}
	dump.Sections = matched
	dump.Text, dump.Redacted = hacfg.Redact(text)

	// The file is only a description of what HAProxy runs if it has not changed since it started.
	if client, err := i.Client(); err == nil {
		if w := client.configDrift(src.Modified); w != "" {
			dump.Warnings = append(dump.Warnings, w)
		}
	}
	return dump, nil
}
