// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"strings"
)

// Plugins get an allowlisted environment, not the whole parent env, so
// unrelated secrets in the user's shell don't leak into every plugin.
// Mirrors entireio/cli's plugin_env.go, plus one addition: a plugin always
// sees variables prefixed with its own upper-cased name (DIGISAC_* for
// mf-digisac), which is where it keeps its own configuration.
//
//nolint:gochecknoglobals // static lookup table
var pluginEnvAllowed = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {},
	"SHELL": {}, "PWD": {}, "TMPDIR": {}, "TZ": {},
	"LANG": {}, "LANGUAGE": {},
	"TERM": {}, "TERM_PROGRAM": {}, "TERM_PROGRAM_VERSION": {},
	"COLORTERM": {}, "NO_COLOR": {}, "FORCE_COLOR": {},
	"CLICOLOR": {}, "CLICOLOR_FORCE": {},
	"CI": {}, "GITHUB_ACTIONS": {},
	"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {}, "ALL_PROXY": {},
	"http_proxy": {}, "https_proxy": {}, "no_proxy": {}, "all_proxy": {},
	"SSH_AUTH_SOCK": {},
}

//nolint:gochecknoglobals // static lookup table
var pluginEnvPrefixes = []string{"MF_", "LC_", "XDG_"}

// pluginEnvOverrideVar lets the user pass extra variables through:
// MF_PLUGIN_ENV="AWS_PROFILE,GITHUB_*".
const pluginEnvOverrideVar = "MF_PLUGIN_ENV"

func pluginEnv(parent []string, pluginName string, extra ...string) []string {
	prefixes := append([]string{pluginEnvPrefix(pluginName)}, pluginEnvPrefixes...)
	exact, userPrefixes := parsePluginEnvOverride(lookupEnv(parent, pluginEnvOverrideVar))
	prefixes = append(prefixes, userPrefixes...)

	out := make([]string, 0, len(parent)+len(extra))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if isPluginEnvAllowed(kv[:eq], exact, prefixes) {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// pluginEnvPrefix turns a plugin name into its env prefix: "my-tool" -> "MY_TOOL_".
func pluginEnvPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

func isPluginEnvAllowed(name string, exact map[string]struct{}, prefixes []string) bool {
	if _, ok := pluginEnvAllowed[name]; ok {
		return true
	}
	if _, ok := exact[name]; ok {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func parsePluginEnvOverride(s string) (exact map[string]struct{}, prefixes []string) {
	exact = map[string]struct{}{}
	for _, raw := range strings.Split(s, ",") {
		item := strings.TrimSpace(raw)
		switch {
		case item == "":
		case strings.HasSuffix(item, "*"):
			prefixes = append(prefixes, strings.TrimSuffix(item, "*"))
		default:
			exact[item] = struct{}{}
		}
	}
	return exact, prefixes
}

func lookupEnv(env []string, name string) string {
	prefix := name + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}
