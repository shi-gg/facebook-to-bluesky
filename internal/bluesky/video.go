package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/bluesky-social/indigo/lex/util"
)

const (
	defaultVideoServiceURL = "https://video.bsky.app"
	videoServiceDID        = "did:web:video.bsky.app"
	videoUploadTTL         = 30 * time.Minute
)

type Video struct {
	Path     string
	Size     int64
	Width    int64
	Height   int64
	Duration time.Duration
	MimeType string
}

type videoLimits struct {
	CanUpload bool   `json:"canUpload"`
	Message   string `json:"message"`
	Error     string `json:"error"`
}

type videoStartUpload struct {
	JobID         string `json:"jobId"`
	PartSizeBytes int64  `json:"partSizeBytes"`
	PartCount     int    `json:"partCount"`
	ExpiresAt     string `json:"expiresAt"`
}

type videoJobStatus struct {
	JobID       string        `json:"jobId"`
	DID         string        `json:"did"`
	State       string        `json:"state"`
	Progress    int           `json:"progress"`
	Blob        *util.LexBlob `json:"blob"`
	Error       string        `json:"error"`
	FailureCode string        `json:"failureCode"`
	Message     string        `json:"message"`
}

type videoFinishUpload struct {
	CompletedJobID string          `json:"completedJobId"`
	JobStatus      *videoJobStatus `json:"jobStatus"`
}

type serviceAuthOutput struct {
	Token string `json:"token"`
}

func (c *Client) serviceAuth(ctx context.Context, aud, lxm string, exp int64) (string, error) {
	params := map[string]any{
		"aud": aud,
		"lxm": lxm,
	}
	if exp > 0 {
		params["exp"] = exp
	}

	var out serviceAuthOutput
	if err := c.api.LexDo(ctx, http.MethodGet, "", "com.atproto.server.getServiceAuth", params, nil, &out); err != nil {
		return "", fmt.Errorf("get service auth: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("get service auth: empty token")
	}

	return out.Token, nil
}

func (c *Client) uploadVideo(ctx context.Context, video *Video) (*util.LexBlob, error) {
	if video == nil {
		return nil, fmt.Errorf("video required")
	}
	if video.Size <= 0 {
		return nil, fmt.Errorf("video is empty")
	}

	limitsToken, err := c.serviceAuth(ctx, videoServiceDID, "app.bsky.video.getUploadLimits", 0)
	if err != nil {
		return nil, err
	}

	var limits videoLimits
	if err := c.videoCall(ctx, limitsToken, http.MethodGet, "getUploadLimits", nil, nil, &limits); err != nil {
		return nil, err
	}
	if !limits.CanUpload {
		if limits.Message != "" {
			return nil, fmt.Errorf("video upload not allowed: %s", limits.Message)
		}
		if limits.Error != "" {
			return nil, fmt.Errorf("video upload not allowed: %s", limits.Error)
		}
		return nil, fmt.Errorf("video upload not allowed")
	}

	if c.pdsAud == "" {
		return nil, fmt.Errorf("unknown PDS audience")
	}

	token, err := c.serviceAuth(ctx, c.pdsAud, "com.atproto.repo.uploadBlob", time.Now().Add(videoUploadTTL).Unix())
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%d.mp4", time.Now().UnixNano())
	body := map[string]any{
		"sizeBytes": video.Size,
		"mimeType":  video.MimeType,
		"name":      name,
	}
	if video.Duration > 0 {
		body["durationMs"] = video.Duration.Milliseconds()
	}
	if video.Width > 0 && video.Height > 0 {
		body["width"] = video.Width
		body["height"] = video.Height
	}

	var started videoStartUpload
	if err := c.videoCall(ctx, token, http.MethodPost, "startUpload", nil, body, &started); err != nil {
		return nil, err
	}
	if started.JobID == "" || started.PartSizeBytes <= 0 || started.PartCount <= 0 {
		return nil, fmt.Errorf("video service returned an invalid upload plan")
	}
	if started.PartSizeBytes*int64(started.PartCount) < video.Size {
		return nil, fmt.Errorf("video service upload plan is too small for the video")
	}

	file, err := os.Open(video.Path)
	if err != nil {
		return nil, fmt.Errorf("open video: %w", err)
	}
	defer file.Close()

	for part := 1; part <= started.PartCount; part++ {
		offset := int64(part-1) * started.PartSizeBytes
		length := started.PartSizeBytes
		if remaining := video.Size - offset; remaining < length {
			length = remaining
		}
		if length <= 0 {
			return nil, fmt.Errorf("invalid upload part %d", part)
		}

		section := io.NewSectionReader(file, offset, length)
		if err := c.uploadVideoPart(ctx, token, started.JobID, part, section, length); err != nil {
			return nil, fmt.Errorf("upload video part %d: %w", part, err)
		}
	}

	var finished videoFinishUpload
	if err := c.videoCall(ctx, token, http.MethodPost, "finishUpload", nil, map[string]any{
		"jobId": started.JobID,
	}, &finished); err != nil {
		return nil, err
	}

	jobID := finished.CompletedJobID
	if jobID == "" {
		jobID = started.JobID
	}
	if finished.JobStatus != nil && finished.JobStatus.State == "JOB_STATE_COMPLETED" && finished.JobStatus.Blob != nil {
		return finished.JobStatus.Blob, nil
	}

	return c.waitForVideo(ctx, jobID)
}

func (c *Client) waitForVideo(ctx context.Context, jobID string) (*util.LexBlob, error) {
	ctx, cancel := context.WithTimeout(ctx, c.videoJobTimeout)
	defer cancel()

	ticker := time.NewTicker(c.videoPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for video processing: %w", ctx.Err())
		case <-ticker.C:
		}

		var out struct {
			JobStatus *videoJobStatus `json:"jobStatus"`
		}
		if err := c.videoCall(ctx, "", http.MethodGet, "getJobStatus", url.Values{"jobId": {jobID}}, nil, &out); err != nil {
			return nil, err
		}
		if out.JobStatus == nil {
			return nil, fmt.Errorf("video service returned no job status")
		}

		switch out.JobStatus.State {
		case "JOB_STATE_COMPLETED":
			if out.JobStatus.Blob == nil {
				return nil, fmt.Errorf("video job completed without a blob")
			}
			return out.JobStatus.Blob, nil
		case "JOB_STATE_FAILED":
			return nil, videoJobError(out.JobStatus)
		}
	}
}

func videoJobError(status *videoJobStatus) error {
	reason := status.Error
	if reason == "" {
		reason = status.Message
	}
	if reason == "" {
		reason = "video processing failed"
	}
	if status.FailureCode != "" {
		return fmt.Errorf("%s (%s)", reason, status.FailureCode)
	}
	return fmt.Errorf("%s", reason)
}

func (c *Client) videoCall(ctx context.Context, token, method, route string, params url.Values, body any, out any) error {
	endpoint := c.videoServiceURL + "/xrpc/app.bsky.video." + route
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode video request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.videoHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("video service %s: %w", route, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return videoServiceError(route, resp)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode video service %s: %w", route, err)
	}

	return nil
}

func (c *Client) uploadVideoPart(ctx context.Context, token, jobID string, partNumber int, body io.Reader, size int64) error {
	params := url.Values{
		"jobId":      {jobID},
		"partNumber": {strconv.Itoa(partNumber)},
	}
	endpoint := c.videoServiceURL + "/xrpc/app.bsky.video.uploadPart?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.videoPartHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("video service uploadPart: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return videoServiceError("uploadPart", resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	return nil
}

func videoServiceError(route string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var xrpc struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &xrpc)

	message := xrpc.Message
	if message == "" {
		message = xrpc.Error
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	return fmt.Errorf("video service %s: HTTP %d: %s", route, resp.StatusCode, message)
}
