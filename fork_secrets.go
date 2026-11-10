package main

// pxi: saved forge tokens never leave the server (px0 issue #268). The
// settings API returns them masked ("••••" and the last four characters), in
// both the settings map and the raw settings.json text, so they do not show in
// the Settings view, a screen share or a screenshot. Saving a masked value back
// (the form, or the raw editor unchanged) keeps the stored token.

import (
	"regexp"
	"strings"

	"px0/internal/settings"
)

const pxiSecretMask = "••••"

// pxiSecretKey: settings that hold a credential.
func pxiSecretKey(k string) bool {
	k = strings.ToLower(k)
	return strings.HasSuffix(k, "token") || strings.HasSuffix(k, "secret") || strings.HasSuffix(k, "password") ||
		strings.HasSuffix(k, "apikey") || strings.HasSuffix(k, "api_key")
}

func pxiMaskValue(v string) string {
	if v == "" {
		return ""
	}
	if len(v) > 8 {
		return pxiSecretMask + v[len(v)-4:]
	}
	return pxiSecretMask
}

// pxiMaskSettings returns m with every secret masked (a copy; m is not changed).
func pxiMaskSettings(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok && pxiSecretKey(k) {
			v = pxiMaskValue(s)
		}
		out[k] = v
	}
	return out
}

// "github.token": "value" in settings.json text.
var pxiSecretRawRe = regexp.MustCompile(`("([^"\\]*)"\s*:\s*")((?:[^"\\]|\\.)*)(")`)

func pxiMaskRaw(raw string) string {
	return pxiSecretRawRe.ReplaceAllStringFunc(raw, func(m string) string {
		p := pxiSecretRawRe.FindStringSubmatch(m)
		if !pxiSecretKey(p[2]) {
			return m
		}
		return p[1] + pxiMaskValue(p[3]) + p[4]
	})
}

// pxiUnmaskPayload drops masked secrets from a settings update, so the stored
// value stays.
func pxiUnmaskPayload(payload map[string]any) map[string]any {
	for k, v := range payload {
		if s, ok := v.(string); ok && pxiSecretKey(k) && strings.HasPrefix(s, pxiSecretMask) {
			delete(payload, k)
		}
	}
	return payload
}

// pxiUnmaskRaw puts the stored secret back wherever the raw text still holds
// its mask.
func pxiUnmaskRaw(raw string) string {
	cur := settings.ReadMergedMap()
	return pxiSecretRawRe.ReplaceAllStringFunc(raw, func(m string) string {
		p := pxiSecretRawRe.FindStringSubmatch(m)
		if !pxiSecretKey(p[2]) || !strings.HasPrefix(p[3], pxiSecretMask) {
			return m
		}
		real, _ := cur[p[2]].(string)
		real = strings.ReplaceAll(strings.ReplaceAll(real, `\`, `\\`), `"`, `\"`)
		return p[1] + real + p[4]
	})
}
