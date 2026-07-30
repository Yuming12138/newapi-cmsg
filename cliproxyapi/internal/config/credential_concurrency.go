package config

import (
	"fmt"
	"time"
)

const (
	defaultCPAHeartbeatTimeout          = 3 * time.Second
	defaultCPACancelBound               = 5 * time.Second
	defaultReclaimGrace                 = 5 * time.Second
	defaultCleanupInterval              = 5 * time.Second
	defaultReleaseFlushInterval         = 250 * time.Millisecond
	defaultReleaseMaxBackoff            = 2 * time.Second
	defaultBusyRetryMin                 = 250 * time.Millisecond
	defaultBusyRetryMax                 = time.Second
	maxCredentialConcurrencyLimit int64 = 1_000_000
)

// CredentialConcurrencyConfig carries Home-authoritative credential lifecycle settings.
type CredentialConcurrencyConfig struct {
	LifecycleConfigRevision    int64         `yaml:"lifecycle-config-revision" json:"lifecycle-config-revision"`
	ObservationBarrierRevision int64         `yaml:"observation-barrier-revision" json:"observation-barrier-revision"`
	CPAHeartbeatTimeout        time.Duration `yaml:"cpa-heartbeat-timeout" json:"cpa-heartbeat-timeout"`
	CPACancelBound             time.Duration `yaml:"cpa-cancel-bound" json:"cpa-cancel-bound"`
	ReclaimGrace               time.Duration `yaml:"reclaim-grace" json:"reclaim-grace"`
	CleanupInterval            time.Duration `yaml:"cleanup-interval" json:"cleanup-interval"`
	ReleaseFlushInterval       time.Duration `yaml:"release-flush-interval" json:"release-flush-interval"`
	ReleaseMaxBackoff          time.Duration `yaml:"release-max-backoff" json:"release-max-backoff"`
	BusyRetryMin               time.Duration `yaml:"busy-retry-min" json:"busy-retry-min"`
	BusyRetryMax               time.Duration `yaml:"busy-retry-max" json:"busy-retry-max"`
	MaxLimit                   int64         `yaml:"max-limit" json:"max-limit"`
}

// WithDefaults applies compatibility defaults for Home versions that do not send all fields.
func (c CredentialConcurrencyConfig) WithDefaults() CredentialConcurrencyConfig {
	if c.CPAHeartbeatTimeout == 0 {
		c.CPAHeartbeatTimeout = defaultCPAHeartbeatTimeout
	}
	if c.CPACancelBound == 0 {
		c.CPACancelBound = defaultCPACancelBound
	}
	if c.ReclaimGrace == 0 {
		c.ReclaimGrace = defaultReclaimGrace
	}
	if c.CleanupInterval == 0 {
		c.CleanupInterval = defaultCleanupInterval
	}
	if c.ReleaseFlushInterval == 0 {
		c.ReleaseFlushInterval = defaultReleaseFlushInterval
	}
	if c.ReleaseMaxBackoff == 0 {
		c.ReleaseMaxBackoff = defaultReleaseMaxBackoff
	}
	if c.BusyRetryMin == 0 {
		c.BusyRetryMin = defaultBusyRetryMin
	}
	if c.BusyRetryMax == 0 {
		c.BusyRetryMax = defaultBusyRetryMax
	}
	if c.MaxLimit == 0 {
		c.MaxLimit = maxCredentialConcurrencyLimit
	}
	return c
}

func ValidateCredentialConcurrency(cfg CredentialConcurrencyConfig) error {
	cfg = cfg.WithDefaults()
	if cfg.LifecycleConfigRevision < 0 {
		return fmt.Errorf("lifecycle configuration revision must not be negative")
	}
	if cfg.ObservationBarrierRevision < 0 {
		return fmt.Errorf("observation barrier revision must not be negative")
	}
	if cfg.CPAHeartbeatTimeout <= 0 || cfg.CPACancelBound <= 0 || cfg.ReclaimGrace <= 0 || cfg.CleanupInterval <= 0 {
		return fmt.Errorf("credential concurrency lifecycle durations must be positive")
	}
	if cfg.ReleaseFlushInterval <= 0 || cfg.ReleaseMaxBackoff <= 0 || cfg.BusyRetryMin <= 0 || cfg.BusyRetryMax <= 0 {
		return fmt.Errorf("credential concurrency limiter durations must be positive")
	}
	if cfg.ReleaseMaxBackoff < cfg.ReleaseFlushInterval {
		return fmt.Errorf("credential concurrency release max backoff must not be less than release flush interval")
	}
	if cfg.BusyRetryMax < cfg.BusyRetryMin {
		return fmt.Errorf("credential concurrency busy retry max must not be less than busy retry min")
	}
	if cfg.MaxLimit < 1 || cfg.MaxLimit > maxCredentialConcurrencyLimit {
		return fmt.Errorf("credential concurrency max limit must be between 1 and %d", maxCredentialConcurrencyLimit)
	}
	return nil
}

func ValidateCredentialConcurrencyLifecycle(nodeHeartbeatTimeout time.Duration, cfg CredentialConcurrencyConfig) error {
	if nodeHeartbeatTimeout <= 0 {
		return fmt.Errorf("credential concurrency lifecycle durations must be positive")
	}
	cfg = cfg.WithDefaults()
	if err := ValidateCredentialConcurrency(cfg); err != nil {
		return err
	}
	if nodeHeartbeatTimeout+cfg.ReclaimGrace <= cfg.CPAHeartbeatTimeout+cfg.CPACancelBound {
		return fmt.Errorf("node heartbeat timeout plus reclaim grace must exceed CPA heartbeat timeout plus cancel bound")
	}
	return nil
}
