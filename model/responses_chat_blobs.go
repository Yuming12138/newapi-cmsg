package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	responsesChatBlobMarkerPrefix = "\x00cmsg_blob_v1:"
	responsesChatBlobDefaultBytes = 256 << 10
)

var responsesChatBlobErrors atomic.Uint64

func responsesChatBlobDirectory() string {
	if configured := strings.TrimSpace(common.GetEnvOrDefaultString("RESPONSES_SESSION_BLOB_DIR", "")); configured != "" {
		return configured
	}
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		return filepath.Join("/data", "responses_session_blobs")
	}
	return filepath.Join(os.TempDir(), "new-api-responses-session-blobs")
}

func responsesChatBlobThreshold() int {
	threshold := common.GetEnvOrDefault("RESPONSES_SESSION_BLOB_THRESHOLD_BYTES", responsesChatBlobDefaultBytes)
	if threshold < 0 {
		return 0
	}
	return threshold
}

func responsesChatBlobPath(hash string) (string, error) {
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("invalid responses session blob hash")
	}
	return filepath.Join(responsesChatBlobDirectory(), hash), nil
}

func writeResponsesChatBlob(data string) (string, error) {
	sum := sha256.Sum256([]byte(data))
	hash := hex.EncodeToString(sum[:])
	path, err := responsesChatBlobPath(hash)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return hash, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".blob-*")
	if err != nil {
		return "", err
	}
	tempPath := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}
	if err := file.Chmod(0600); err != nil {
		cleanup()
		return "", err
	}
	if _, err := file.WriteString(data); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Rename(tempPath, path); err != nil {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() == int64(len(data)) {
			_ = os.Remove(tempPath)
			return hash, nil
		}
		_ = os.Remove(tempPath)
		return "", err
	}
	_ = os.Chmod(path, 0600)
	return hash, nil
}

func externalizeResponsesChatValue(value any, threshold int) (any, bool, error) {
	switch typed := value.(type) {
	case string:
		if threshold == 0 || len(typed) < threshold || strings.HasPrefix(typed, responsesChatBlobMarkerPrefix) {
			return typed, false, nil
		}
		hash, err := writeResponsesChatBlob(typed)
		if err != nil {
			responsesChatBlobErrors.Add(1)
			return typed, false, nil
		}
		return responsesChatBlobMarkerPrefix + hash + ":" + strconv.Itoa(len(typed)), true, nil
	case []any:
		changed := false
		result := make([]any, len(typed))
		for index, item := range typed {
			next, itemChanged, err := externalizeResponsesChatValue(item, threshold)
			if err != nil {
				return value, false, err
			}
			result[index] = next
			changed = changed || itemChanged
		}
		if !changed {
			return value, false, nil
		}
		return result, true, nil
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			next, itemChanged, err := externalizeResponsesChatValue(item, threshold)
			if err != nil {
				return value, false, err
			}
			result[key] = next
			changed = changed || itemChanged
		}
		if !changed {
			return value, false, nil
		}
		return result, true, nil
	default:
		return value, false, nil
	}
}

func externalizeResponsesChatPayload(data string) (string, error) {
	threshold := responsesChatBlobThreshold()
	if threshold == 0 || len(data) < threshold {
		return data, nil
	}
	var value any
	if err := common.UnmarshalJsonStr(data, &value); err != nil {
		return "", err
	}
	next, changed, err := externalizeResponsesChatValue(value, threshold)
	if err != nil || !changed {
		return data, err
	}
	encoded, err := common.Marshal(next)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func readResponsesChatBlob(hash string, expectedSize int) (string, error) {
	if expectedSize < 0 || expectedSize > responsesChatMaxPayloadBytes() {
		return "", errors.New("responses session blob exceeds size limit")
	}
	path, err := responsesChatBlobPath(hash)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if expectedSize >= 0 && info.Size() != int64(expectedSize) {
		return "", fmt.Errorf("responses session blob size mismatch: %s", hash)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return "", fmt.Errorf("responses session blob hash mismatch: %s", hash)
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return string(data), nil
}

func hydrateResponsesChatValue(value any) (any, bool, error) {
	switch typed := value.(type) {
	case string:
		if !strings.HasPrefix(typed, responsesChatBlobMarkerPrefix) {
			return typed, false, nil
		}
		parts := strings.Split(strings.TrimPrefix(typed, responsesChatBlobMarkerPrefix), ":")
		if len(parts) != 2 {
			return nil, false, errors.New("invalid responses session blob marker")
		}
		expectedSize, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, false, errors.New("invalid responses session blob size")
		}
		data, err := readResponsesChatBlob(parts[0], expectedSize)
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	case []any:
		changed := false
		result := make([]any, len(typed))
		for index, item := range typed {
			next, itemChanged, err := hydrateResponsesChatValue(item)
			if err != nil {
				return nil, false, err
			}
			result[index] = next
			changed = changed || itemChanged
		}
		if !changed {
			return value, false, nil
		}
		return result, true, nil
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			next, itemChanged, err := hydrateResponsesChatValue(item)
			if err != nil {
				return nil, false, err
			}
			result[key] = next
			changed = changed || itemChanged
		}
		if !changed {
			return value, false, nil
		}
		return result, true, nil
	default:
		return value, false, nil
	}
}

func hydrateResponsesChatPayload(data string) (string, error) {
	if !strings.Contains(data, "cmsg_blob_v1:") {
		return data, nil
	}
	var value any
	if err := common.UnmarshalJsonStr(data, &value); err != nil {
		return "", err
	}
	next, changed, err := hydrateResponsesChatValue(value)
	if err != nil || !changed {
		return data, err
	}
	encoded, err := common.Marshal(next)
	if err != nil {
		return "", err
	}
	payload := string(encoded)
	if err := validateResponsesChatPayloadSize("hydrated responses session", payload); err != nil {
		return "", err
	}
	return payload, nil
}

func cleanupResponsesChatBlobs(cutoff int64) error {
	dir := responsesChatBlobDirectory()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) != sha256.Size*2 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Unix() < cutoff {
			_ = os.Remove(path)
		}
	}
	return nil
}
