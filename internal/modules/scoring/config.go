package scoring

import (
	"encoding/json"
	"fmt"
)

// Weights are the spec §8 component weights; they must sum to 1.
type Weights struct {
	Weather float64 `json:"weather"`
	Crowd   float64 `json:"crowd"`
	Amenity float64 `json:"amenity"`
	Shade   float64 `json:"shade"`
	Fresh   float64 `json:"fresh"`
}

// Thresholds drive the hard caps (spec §8: rain/extreme temps cap the score).
type Thresholds struct {
	RainProbThreshold int     `json:"rain_prob_threshold"`
	RainScoreCap      int     `json:"rain_score_cap"`
	HeatCapC          float64 `json:"heat_cap_c"`
	HeatScoreCap      int     `json:"heat_score_cap"`
	ColdCapC          float64 `json:"cold_cap_c"`
	ColdScoreCap      int     `json:"cold_score_cap"`
}

// Config is a versioned scoring configuration loaded from scoring_config.
type Config struct {
	Version    int16
	Weights    Weights
	Thresholds Thresholds
}

func ParseConfig(version int16, weightsJSON, thresholdsJSON []byte) (Config, error) {
	var cfg Config
	cfg.Version = version
	if err := json.Unmarshal(weightsJSON, &cfg.Weights); err != nil {
		return Config{}, fmt.Errorf("weights: %w", err)
	}
	if err := json.Unmarshal(thresholdsJSON, &cfg.Thresholds); err != nil {
		return Config{}, fmt.Errorf("thresholds: %w", err)
	}
	return cfg, nil
}
