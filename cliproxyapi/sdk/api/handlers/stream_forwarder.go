package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	log "github.com/sirupsen/logrus"
)

type StreamForwardOptions struct {
	// KeepAliveInterval overrides the configured streaming keep-alive interval.
	// If nil, the configured default is used. If set to <= 0, keep-alives are disabled.
	KeepAliveInterval *time.Duration

	// WriteChunk writes a single data chunk to the response body. It should not flush.
	WriteChunk func(chunk []byte)

	// WriteTerminalError writes an error payload to the response body when streaming fails
	// after headers have already been committed. It should not flush.
	WriteTerminalError func(errMsg *interfaces.ErrorMessage)

	// WriteDone optionally writes a terminal marker when the upstream data channel closes
	// without an error (e.g. OpenAI's `[DONE]`). It should not flush.
	WriteDone func()

	// WriteKeepAlive optionally writes a keep-alive heartbeat. It should not flush.
	// When nil, a standard SSE comment heartbeat is used.
	WriteKeepAlive func()
}

func (h *BaseAPIHandler) ForwardStream(c *gin.Context, flusher http.Flusher, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage, opts StreamForwardOptions) {
	if c == nil {
		return
	}
	if cancel == nil {
		return
	}

	metrics := streamForwardMetrics{startedAt: time.Now()}
	writeChunk := opts.WriteChunk
	if writeChunk == nil {
		writeChunk = func([]byte) {}
	}

	writeKeepAlive := opts.WriteKeepAlive
	if writeKeepAlive == nil {
		writeKeepAlive = func() {
			_, _ = c.Writer.Write([]byte(": keep-alive\n\n"))
		}
	}

	keepAliveInterval := StreamingKeepAliveInterval(h.Cfg)
	if opts.KeepAliveInterval != nil {
		keepAliveInterval = *opts.KeepAliveInterval
	}
	var keepAlive *time.Ticker
	var keepAliveC <-chan time.Time
	if keepAliveInterval > 0 {
		keepAlive = time.NewTicker(keepAliveInterval)
		defer keepAlive.Stop()
		keepAliveC = keepAlive.C
	}

	var terminalErr *interfaces.ErrorMessage
	for {
		select {
		case <-c.Request.Context().Done():
			logStreamForwardEvent(c, metrics, "client_canceled", nil, log.InfoLevel)
			cancel(c.Request.Context().Err())
			return
		case chunk, ok := <-data:
			if !ok {
				// Prefer surfacing a terminal error if one is pending.
				if terminalErr == nil {
					select {
					case errMsg, ok := <-errs:
						if ok && errMsg != nil {
							terminalErr = errMsg
						}
					default:
					}
				}
				if terminalErr != nil {
					if opts.WriteTerminalError != nil {
						opts.WriteTerminalError(terminalErr)
					}
					flusher.Flush()
					logStreamForwardEvent(c, metrics, "terminal_error", terminalErr, log.WarnLevel)
					cancel(terminalErr.Error)
					return
				}
				if opts.WriteDone != nil {
					opts.WriteDone()
				}
				flusher.Flush()
				logStreamForwardEvent(c, metrics, "completed", nil, log.InfoLevel)
				cancel(nil)
				return
			}
			metrics.recordPayload(chunk)
			if metrics.payloadChunks == 1 {
				logStreamForwardFirstPayload(c, metrics, len(chunk))
			}
			writeChunk(chunk)
			flusher.Flush()
		case errMsg, ok := <-errs:
			if !ok {
				continue
			}
			if errMsg != nil {
				terminalErr = errMsg
				if opts.WriteTerminalError != nil {
					opts.WriteTerminalError(errMsg)
					flusher.Flush()
				}
			}
			var execErr error
			if errMsg != nil {
				execErr = errMsg.Error
			}
			logStreamForwardEvent(c, metrics, "terminal_error", errMsg, log.WarnLevel)
			cancel(execErr)
			return
		case <-keepAliveC:
			metrics.keepAliveChunks++
			writeKeepAlive()
			flusher.Flush()
		}
	}
}

type streamForwardMetrics struct {
	startedAt       time.Time
	firstPayloadAt  time.Time
	payloadChunks   int64
	payloadBytes    int64
	keepAliveChunks int64
}

func (m *streamForwardMetrics) recordPayload(chunk []byte) {
	if m == nil {
		return
	}
	now := time.Now()
	if m.firstPayloadAt.IsZero() {
		m.firstPayloadAt = now
	}
	m.payloadChunks++
	m.payloadBytes += int64(len(chunk))
}

func logStreamForwardFirstPayload(c *gin.Context, metrics streamForwardMetrics, chunkBytes int) {
	fields := streamForwardFields(c, metrics)
	fields["chunk_bytes"] = chunkBytes
	streamForwardLogEntry(c).WithFields(fields).Info("stream forward: first downstream payload")
}

func logStreamForwardEvent(c *gin.Context, metrics streamForwardMetrics, reason string, errMsg *interfaces.ErrorMessage, level log.Level) {
	fields := streamForwardFields(c, metrics)
	fields["reason"] = reason
	if errMsg != nil {
		fields["status_code"] = errMsg.StatusCode
	}
	entry := streamForwardLogEntry(c).WithFields(fields)
	switch level {
	case log.WarnLevel:
		entry.Warn("stream forward: finished")
	case log.ErrorLevel:
		entry.Error("stream forward: finished")
	default:
		entry.Info("stream forward: finished")
	}
}

func streamForwardFields(c *gin.Context, metrics streamForwardMetrics) log.Fields {
	fields := log.Fields{
		"duration_ms":      durationMilliseconds(time.Since(metrics.startedAt)),
		"payload_chunks":   metrics.payloadChunks,
		"payload_bytes":    metrics.payloadBytes,
		"keepalive_chunks": metrics.keepAliveChunks,
	}
	if metrics.firstPayloadAt.IsZero() {
		fields["first_payload_ms"] = -1
	} else {
		fields["first_payload_ms"] = durationMilliseconds(metrics.firstPayloadAt.Sub(metrics.startedAt))
	}
	if c != nil && c.Request != nil {
		fields["method"] = c.Request.Method
		if c.Request.URL != nil {
			fields["path"] = c.Request.URL.Path
		}
	}
	return fields
}

func durationMilliseconds(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

func streamForwardLogEntry(c *gin.Context) *log.Entry {
	if c == nil || c.Request == nil {
		return log.NewEntry(log.StandardLogger())
	}
	if requestID := logging.GetRequestID(c.Request.Context()); requestID != "" {
		return log.WithField("request_id", requestID)
	}
	return log.NewEntry(log.StandardLogger())
}
