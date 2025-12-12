package wimt

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/adler32"
	"io"
	"math/rand/v2"

	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	baseURL    = "https://whereismytrain.in/cache/live_status"
	appVersion = "7.1.5.802422502"
	staticUID  = "caea2ea591b5446f82acbf4db26b7c13"
)

// returns a hex string of length 2*byteLen
func generateHexID(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := crand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// computes adler-32 checksum and returns it as a decimal string
func computeAdler32String(s string) string {
	sum := adler32.Checksum([]byte(s))
	return strconv.FormatUint(uint64(sum), 10)
}

// generates the wid parameter for api requests
func generateWID(uid, version, qid, trainNo, from, to, date, fromDay string) string {
	input := fmt.Sprintf("%s%s%s%s%s%s%s%s", uid, version, qid, trainNo, from, to, date, fromDay)
	return computeAdler32String(input)
}

// android user-agents for various popular devices in india
var userAgents = []string{
	// samsung
	"Dalvik/2.1.0 (Linux; U; Android 13; SM-A135F Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 12; SM-M32 Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 13; SM-A235F Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 11; SM-A125F Build/RP1A.200720.012)",
	"Dalvik/2.1.0 (Linux; U; Android 12; SM-A52s Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 13; SM-M33 Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 12; SM-F127G Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 11; SM-A31 Build/RP1A.200720.012)",
	// xiaomi/redmi
	"Dalvik/2.1.0 (Linux; U; Android 13; Redmi Note 12 Build/TKQ1.221114.001)",
	"Dalvik/2.1.0 (Linux; U; Android 12; Redmi Note 11 Build/SKQ1.211006.001)",
	"Dalvik/2.1.0 (Linux; U; Android 11; Redmi 9 Power Build/RP1A.200720.011)",
	"Dalvik/2.1.0 (Linux; U; Android 13; POCO M5 Build/TKQ1.221114.001)",
	"Dalvik/2.1.0 (Linux; U; Android 12; Redmi 10 Build/SKQ1.211006.001)",
	"Dalvik/2.1.0 (Linux; U; Android 13; Redmi Note 12 Pro Build/TKQ1.221114.001)",
	"Dalvik/2.1.0 (Linux; U; Android 11; Redmi 9A Build/RP1A.200720.011)",
	"Dalvik/2.1.0 (Linux; U; Android 12; POCO X4 Pro Build/SKQ1.211006.001)",
	// vivo
	"Dalvik/2.1.0 (Linux; U; Android 13; vivo Y22 Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 12; vivo Y75 Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 11; vivo Y20 Build/RP1A.200720.012)",
	"Dalvik/2.1.0 (Linux; U; Android 13; vivo V27 Build/TP1A.220624.014)",
	// oppo
	"Dalvik/2.1.0 (Linux; U; Android 13; CPH2465 Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 12; CPH2219 Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 11; CPH2185 Build/RP1A.200720.012)",
	"Dalvik/2.1.0 (Linux; U; Android 13; CPH2531 Build/TP1A.220624.014)",
	// realme
	"Dalvik/2.1.0 (Linux; U; Android 13; RMX3511 Build/TP1A.220624.014)",
	"Dalvik/2.1.0 (Linux; U; Android 12; RMX3231 Build/SP1A.210812.016)",
	"Dalvik/2.1.0 (Linux; U; Android 11; RMX2185 Build/RP1A.200720.012)",
	// oneplus
	"Dalvik/2.1.0 (Linux; U; Android 13; CPH2449 Build/TP1A.220624.014)",
	// motorola
	"Dalvik/2.1.0 (Linux; U; Android 12; moto g52 Build/S1RTS32.38-132-9)",
	// google pixel
	"Dalvik/2.1.0 (Linux; U; Android 14; Pixel 7 Build/UP1A.231005.007)",
}

// handles requests to the whereismytrain api
type APIClient struct {
	client   *http.Client
	proxyURL string
}

func NewAPIClient(proxyURL string) *APIClient {
	transport := &http.Transport{}

	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxy)
		}
	}

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	return &APIClient{
		client:   client,
		proxyURL: proxyURL,
	}
}

func (c *APIClient) FetchTrainStatus(ctx context.Context, trainNo, fromStn, toStn string, startDate time.Time) ([]byte, error) {
	// generate request identifiers
	qid, err := generateHexID(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate qid: %w", err)
	}

	dateStr := startDate.Format("02-01-2006")
	wid := generateWID(staticUID, appVersion, qid, trainNo, fromStn, toStn, dateStr, "1")

	params := url.Values{}
	params.Set("train_no", trainNo)
	params.Set("date", dateStr)
	params.Set("appVersion", appVersion)
	params.Set("from_day", "1")
	params.Set("wid", wid)
	params.Set("from", fromStn)
	params.Set("to", toStn)
	params.Set("lang", "en")
	params.Set("user", staticUID)
	params.Set("qid", qid)
	params.Set("flow", "regular")
	params.Set("cb", strconv.FormatInt(time.Now().UnixNano(), 10))

	fullURL := baseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgents[rand.IntN(len(userAgents))])
	req.Header.Set("X-Requested-With", "com.whereismytrain.android")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return body, nil
}
