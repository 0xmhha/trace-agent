package usage

import (
	"strings"
	"testing"
)

// Samples are assembled at run time so that no secret-shaped literal is
// committed to the repository.
func TestMask(t *testing.T) {
	filler := strings.Repeat("A", 24)
	hosting := "gh" + "p_" + filler
	curlUser := "curl -" + "u admin:"
	for _, tc := range []struct {
		name, in, want string
		count          int
	}{
		{"plain text is unchanged", "go test ./... passed in 3s", "go test ./... passed in 3s", 0},
		{"cloud access key", "key " + "AK" + "IA" + strings.Repeat("B", 16) + " end", "key " + MaskToken + " end", 1},
		{"temporary cloud key", "AS" + "IA" + strings.Repeat("C", 16), MaskToken, 1},
		{"model api key", "s" + "k-" + filler, MaskToken, 1},
		{"hosting token", hosting, MaskToken, 1},
		{"bearer keeps scheme", "Authorization: Bearer " + filler, "Authorization: Bearer " + MaskToken, 1},
		{"basic auth header", "Authorization: Basic " + "dXNlcjpwYXNzd29yZA==", "Authorization: Basic " + MaskToken, 1},
		{"assignment keeps name", "DB_PASS" + "WORD=" + "hunter2222", "DB_PASS" + "WORD=" + MaskToken, 1},
		{"short env name", "DB_" + "PASS=" + "hunter2222", "DB_" + "PASS=" + MaskToken, 1},
		{"json field keeps name", `{"api_key": "` + filler + `"}`, `{"api_key": ` + MaskToken + `}`, 1},
		{"escaped quotes in json", `{"command":"export API_TO` + `KEN=\"abc def\""}`, `{"command":"export API_TO` + `KEN=` + MaskToken + `"}`, 1},
		{"url credentials keep host", "postgres://app:" + "s3cretvalue" + "@db.internal/x", "postgres://app:" + MaskToken + "@db.internal/x", 1},
		{"url token keeps host", "https://" + strings.Repeat("t", 24) + "@example.com/repo.git", "https://" + MaskToken + "@example.com/repo.git", 1},
		{"command line flag", "deploy --pass" + "word " + "hunter2222 --verbose", "deploy --pass" + "word " + MaskToken + " --verbose", 1},
		{"private key flag", "sign --private-key 0x" + strings.Repeat("a", 64), "sign --private-key " + MaskToken, 1},
		{"curl user", curlUser + "hunter2222 https://example.com", curlUser + MaskToken + " https://example.com", 1},
		{"private key block", "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----", MaskToken, 1},
		{"two secrets", hosting + " and " + "s" + "k-" + filler, MaskToken + " and " + MaskToken, 2},
		{"masked value is counted once", "API_TO" + "KEN=" + hosting, "API_TO" + "KEN=" + MaskToken, 1},
		{"already masked text is unchanged", "DB_PASS" + "WORD=" + MaskToken, "DB_PASS" + "WORD=" + MaskToken, 0},
		{"token counts stay readable", `"input_tokens": 15234, "max_tokens"=4096`, `"input_tokens": 15234, "max_tokens"=4096`, 0},
		{"code stays readable", "const token = await getToken()", "const token = await getToken()", 0},
		{"type annotation stays readable", "password: Optional[str]", "password: Optional[str]", 0},
		{"resource names stay readable", "secretName: my-app-tls", "secretName: my-app-tls", 0},
		{"endpoints stay readable", "tokenEndpoint: https://example.com/oauth", "tokenEndpoint: https://example.com/oauth", 0},
		{"tokenizer name stays readable", `"tokenizer": "cl100k_base"`, `"tokenizer": "cl100k_base"`, 0},
		{"prose stays readable", "The token: usually short-lived", "The token: usually short-lived", 0},
		{"transaction hash stays readable", "tx 0x" + strings.Repeat("b", 64) + " mined", "tx 0x" + strings.Repeat("b", 64) + " mined", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, n := Mask(tc.in)
			if got != tc.want || n != tc.count {
				t.Fatalf("Mask(%q) = %q, %d; want %q, %d", tc.in, got, n, tc.want, tc.count)
			}
		})
	}
}

func TestSecretKey(t *testing.T) {
	for name, want := range map[string]bool{
		"token":         true,
		"api_key":       true,
		"DB_PASSWORD":   true,
		"db_pass":       true,
		"client_secret": true,
		"input_tokens":  false,
		"token_count":   false,
		"secretName":    false,
		"step":          false,
		"region":        false,
	} {
		if got := SecretKey(name); got != want {
			t.Errorf("SecretKey(%q) = %v; want %v", name, got, want)
		}
	}
}
