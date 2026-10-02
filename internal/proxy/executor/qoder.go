package executor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"9router/proxy/internal/proxy"
)

const qoderRSAPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

func parseQoderPublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(qoderRSAPublicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not RSA public key")
	}
	return rsaPub, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

func aesEncryptCBCBase64(plaintext []byte, key []byte) (string, error) {
	if len(key) != 16 {
		return "", fmt.Errorf("aes key must be 16 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, key)
	mode.CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func rsaEncryptBase64(data []byte) (string, error) {
	pub, err := parseQoderPublicKey()
	if err != nil {
		return "", err
	}
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, pub, data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func md5Hex(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

// qoderCosySigPath mirrors upstream shared/qoder/cosy.js computeSigPath: the
// request pathname with the leading "/algo" stripped.
func qoderCosySigPath(requestURL string) string {
	u, err := url.Parse(requestURL)
	if err != nil {
		return requestURL
	}
	pathname := u.Path
	if pathname == "" {
		pathname = requestURL
	}
	return strings.TrimPrefix(pathname, "/algo")
}

// QoderCosyCreds carries the identity a COSY signature is bound to. Mirrors
// upstream's `creds` argument (open-sse/shared/qoder/cosy.js buildCosyHeaders).
// UserID and MachineID come from the connection's providerSpecificData, not
// from the request: Qoder rejects a signature made with any other account.
type QoderCosyCreds struct {
	UserID    string
	AuthToken string
	Name      string
	Email     string
	MachineID string
}

// BuildQoderCosyHeaders signs a Qoder request for any COSY path (chat and the
// model list share the scheme). Exported for the dashboard key-validate probe.
func BuildQoderCosyHeaders(body []byte, requestURL string, creds QoderCosyCreds) (map[string]string, error) {
	return buildQoderCosyHeaders(body, requestURL, creds)
}

func buildQoderCosyHeaders(body []byte, requestURL string, creds QoderCosyCreds) (map[string]string, error) {
	// Upstream throws on a missing user id or token rather than inventing one:
	// a signature over a made-up account is rejected with
	// 403 {"code":"105","message":"Login expired"}, which reads as an expired
	// login instead of the real problem (a connection that never stored its
	// userId). Failing here names it.
	if creds.UserID == "" {
		return nil, fmt.Errorf("qoder: COSY signing needs the account user id — re-authorize this connection")
	}
	if creds.AuthToken == "" {
		return nil, fmt.Errorf("qoder: COSY signing needs an auth token")
	}
	userID, token := creds.UserID, creds.AuthToken

	aesKeyStr := uuid.New().String()[:16]
	aesKey := []byte(aesKeyStr)

	userInfoJSON, err := json.Marshal(map[string]string{
		"uid":                  userID,
		"security_oauth_token": token,
		"name":                 creds.Name,
		"aid":                  "",
		"email":                creds.Email,
	})
	if err != nil {
		return nil, err
	}

	infoB64, err := aesEncryptCBCBase64(userInfoJSON, aesKey)
	if err != nil {
		return nil, err
	}

	cosyKeyB64, err := rsaEncryptBase64(aesKey)
	if err != nil {
		return nil, err
	}

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	reqID := uuid.New().String()

	payloadJSON, _ := json.Marshal(map[string]string{
		"version":     "v1",
		"requestId":   reqID,
		"info":        infoB64,
		"cosyVersion": "1.0.0",
		"ideVersion":  "",
	})
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)

	sigPath := qoderCosySigPath(requestURL)
	sigInput := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", payloadB64, cosyKeyB64, timestamp, string(body), sigPath)
	sig := md5Hex([]byte(sigInput))

	// Upstream persists the machine UUID on the connection so every request
	// from one auth presents the same machine (cosy.js generateMachineId).
	machineID := creds.MachineID
	if machineID == "" {
		machineID = uuid.New().String()
	}
	bodyHash := md5Hex(body)
	bodyLength := fmt.Sprintf("%d", len(body))

	headers := map[string]string{
		"Authorization":          "Bearer COSY." + payloadB64 + "." + sig,
		"Cosy-Key":               cosyKeyB64,
		"Cosy-User":              userID,
		"Cosy-Date":              timestamp,
		"Cosy-Version":           "1.0.0",
		"Cosy-Machineid":         machineID,
		"Cosy-Machinetoken":      machineID,
		"Cosy-Machinetype":       "5",
		"Cosy-Machineos":         "x86_64_windows",
		"Cosy-Clienttype":        "5",
		"Cosy-Clientip":          "127.0.0.1",
		"Cosy-Bodyhash":          bodyHash,
		"Cosy-Bodylength":        bodyLength,
		"Cosy-Sigpath":           sigPath,
		"Cosy-Data-Policy":       "disagree",
		"Cosy-Organization-Id":   "",
		"Cosy-Organization-Tags": "",
		"Login-Version":          "v2",
		"X-Request-Id":           uuid.New().String(),
	}

	return headers, nil
}

// qoderCosyCreds reads the signing identity off the connection's
// providerSpecificData. Upstream does the same (qoderModels.js
// cosyCredsFromConnection), and the userId is not optional: it is the account
// the signature is verified against, so a connection without one cannot sign
// a usable request.
func qoderCosyCreds(psd map[string]any, token string) QoderCosyCreds {
	creds := QoderCosyCreds{AuthToken: token}
	if psd == nil {
		return creds
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := psd[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	creds.UserID = pick("userId", "user_id", "id")
	creds.MachineID = pick("machineId", "machine_id")
	creds.Name = pick("name", "displayName")
	creds.Email = pick("email")
	return creds
}

// ForwardQoder handles requests for Qoder using COSY signing. The chat
// endpoint comes from the provider's own registry config, so Qoder and
// Qoder CN each talk to their own gateway (upstream registry transport.baseUrl)
// without either provider being aliased onto the other.
func ForwardQoder(w http.ResponseWriter, req *Request) error {
	endpoint := ""
	if req.Config != nil {
		endpoint = req.Config.BaseURL
	}
	if endpoint == "" {
		endpoint = "https://api3.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation"
	}
	headers, err := buildQoderCosyHeaders(req.Body, endpoint, qoderCosyCreds(req.ConnData, req.APIKey))
	if err != nil {
		return fmt.Errorf("build Qoder COSY headers: %w", err)
	}

	targetURL := endpoint + "?FetchKeys=llm_model_result&AgentId=agent_common"
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := proxy.DoRequest(ctx, req.Client, "POST", targetURL, headers, req.Body)
	if err != nil {
		return fmt.Errorf("ForwardQoder: %w", err)
	}
	defer resp.Body.Close()

	if req.IsStream {
		return execSSEStream(w, resp.Body, req)
	}
	return qoderNonStream(w, req, resp.Body)
}

// maxQoderSSEBytes caps the buffered read. Qoder answers from an SSE endpoint
// whatever `stream` says, so a non-streaming request still has to hold the
// stream in memory; the cap keeps a slow or endless upstream from growing the
// heap without bound, matching the codex non-streaming path.
const maxQoderSSEBytes = 10 << 20

// qoderNonStream answers a `stream:false` request. jsonResponse already folds
// an event stream into one chat.completion; the step before it exists because
// Qoder reports failures *inside* that stream, and folding alone would turn a
// real upstream error into a successful empty completion (issue #41).
func qoderNonStream(w http.ResponseWriter, req *Request, upstream io.Reader) error {
	body, err := io.ReadAll(io.LimitReader(upstream, maxQoderSSEBytes))
	if err != nil {
		return fmt.Errorf("ForwardQoder read response: %w", err)
	}
	if err := qoderSSEUpstreamError(body); err != nil {
		return err
	}
	return jsonResponse(req.Ctx, w, bytes.NewReader(body), req.TranslateResp, req.ResponseBuf)
}

// qoderSSEUpstreamError surfaces the failure Qoder hides inside its
// always-SSE response. A `data:` frame carries an envelope shaped
// {"statusCodeValue":400,"statusCode":"BAD_REQUEST","body":"{...}"} that used
// to be written verbatim under an `application/json` header: the client got
// HTTP 200, JSON.parse failed on the SSE text, and the real upstream error was
// never readable. Only a non-streaming request is affected — a streaming one
// passes the frames through and the client sees them.
func qoderSSEUpstreamError(body []byte) error {
	for _, frame := range sseDataFrames(body) {
		var envelope struct {
			StatusCodeValue int    `json:"statusCodeValue"`
			Body            string `json:"body"`
		}
		if err := json.Unmarshal(frame, &envelope); err != nil {
			continue
		}
		if envelope.StatusCodeValue < 400 {
			continue
		}
		message := strings.TrimSpace(envelope.Body)
		if message == "" {
			message = "qoder upstream error"
		}
		return &proxy.UpstreamError{
			StatusCode: envelope.StatusCodeValue,
			Body:       qoderErrorBody(message, envelope.StatusCodeValue),
		}
	}
	return nil
}

// qoderErrorBody wraps an upstream message in the OpenAI error envelope the
// gateway already speaks, so the client reads it instead of SSE text.
func qoderErrorBody(message string, status int) []byte {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "upstream_error",
			"code":    status,
		},
	})
	return body
}
