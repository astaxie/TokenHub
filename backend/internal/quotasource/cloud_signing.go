// Cloud signing adapted from Magpie (MIT), pinned in THIRD_PARTY_NOTICES.
package quotasource

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	volcOpenAPIHost = "open.volcengineapi.com"
	volcContentType = "application/json"
	volcService     = "ark"
	tcContentType   = "application/json; charset=utf-8"
)

func volcSign(ak, sk, region string, query url.Values, body []byte, now time.Time) (auth, xDate, contentSHA string) {
	xDate = now.UTC().Format("20060102T150405Z")
	date := xDate[:8]
	contentSHA = volcHex(body)
	const signed = "content-type;host;x-content-sha256;x-date"
	canonical := strings.Join([]string{
		http.MethodPost,
		"/",
		volcQuery(query),
		"content-type:" + volcContentType + "\n" +
			"host:" + volcOpenAPIHost + "\n" +
			"x-content-sha256:" + contentSHA + "\n" +
			"x-date:" + xDate + "\n",
		signed,
		contentSHA,
	}, "\n")
	scope := date + "/" + region + "/" + volcService + "/request"
	toSign := strings.Join([]string{"HMAC-SHA256", xDate, scope, volcHex([]byte(canonical))}, "\n")
	key := volcMAC([]byte(sk), date)
	for _, s := range []string{region, volcService, "request"} {
		key = volcMAC(key, s)
	}
	sig := hex.EncodeToString(volcMAC(key, toSign))
	return "HMAC-SHA256 Credential=" + ak + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + sig, xDate, contentSHA
}

func volcQuery(v url.Values) string {
	return strings.ReplaceAll(v.Encode(), "+", "%20")
}

func volcMAC(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func volcHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func tc3Sign(secretID, secretKey, service, host, action string, payload []byte, ts time.Time) string {
	date := ts.UTC().Format("2006-01-02")
	const signed = "content-type;host;x-tc-action"
	canonical := strings.Join([]string{
		http.MethodPost,
		"/",
		"",
		"content-type:" + tcContentType + "\n" +
			"host:" + host + "\n" +
			"x-tc-action:" + strings.ToLower(action) + "\n",
		signed,
		volcHex(payload),
	}, "\n")
	scope := date + "/" + service + "/tc3_request"
	toSign := strings.Join([]string{"TC3-HMAC-SHA256", strconv.FormatInt(ts.Unix(), 10), scope, volcHex([]byte(canonical))}, "\n")
	key := volcMAC([]byte("TC3"+secretKey), date)
	key = volcMAC(key, service)
	key = volcMAC(key, "tc3_request")
	sig := hex.EncodeToString(volcMAC(key, toSign))
	return "TC3-HMAC-SHA256 Credential=" + secretID + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + sig
}

func aliACSSign(ak, sk, host, path, action, version string, body []byte, now time.Time, nonce string) map[string]string {
	sum := sha256.Sum256(body)
	hashed := hex.EncodeToString(sum[:])
	h := map[string]string{
		"host":                  host,
		"x-acs-action":          action,
		"x-acs-version":         version,
		"x-acs-date":            now.UTC().Format("2006-01-02T15:04:05Z"),
		"x-acs-signature-nonce": nonce,
		"x-acs-content-sha256":  hashed,
		"content-type":          "application/json",
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var ch strings.Builder
	for _, k := range keys {
		ch.WriteString(k + ":" + h[k] + "\n")
	}
	signed := strings.Join(keys, ";")
	canonical := strings.Join([]string{http.MethodPost, path, "", ch.String(), signed, hashed}, "\n")
	csum := sha256.Sum256([]byte(canonical))
	toSign := "ACS3-HMAC-SHA256\n" + hex.EncodeToString(csum[:])
	m := hmac.New(sha256.New, []byte(sk))
	m.Write([]byte(toSign))
	h["authorization"] = "ACS3-HMAC-SHA256 Credential=" + ak + ",SignedHeaders=" + signed + ",Signature=" + hex.EncodeToString(m.Sum(nil))
	return h
}
