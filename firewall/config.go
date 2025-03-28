package firewall

import (
	"fmt"
	"netrunner/internal"
	"netrunner/types"
	"os"

	"gopkg.in/yaml.v3"
)

type Firewall struct {
	Enabled           bool
	Type              types.FirewallType
	Model             internal.Model
	BlockingThreshold float32
}

type Config struct {
	Firewalls []Firewall
}

type rawFirewall struct {
	Enabled           bool    `yaml:"enabled"`
	Type              string  `yaml:"type"`
	Model             string  `yaml:"model"`
	BlockingThreshold float32 `yaml:"blockingThreshold"`
}

type rawConfig struct {
	Firewalls []rawFirewall `yaml:"firewalls"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw rawConfig
	err = yaml.Unmarshal(data, &raw)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	for _, rf := range raw.Firewalls {
		ft, err := types.NewFirewallType(rf.Type)
		if err != nil {
			return Config{}, fmt.Errorf("invalid firewall type: %w", err)
		}

		modelName, err := types.NewInternalModelName(rf.Model)
		if err != nil {
			return Config{}, fmt.Errorf("invalid model name: %w", err)
		}

		model, err := internal.GetModel(modelName)
		if err != nil {
			return Config{}, fmt.Errorf("failed to get model: %w", err)
		}

		cfg.Firewalls = append(cfg.Firewalls, Firewall{
			Enabled:           rf.Enabled,
			Type:              ft,
			Model:             model,
			BlockingThreshold: rf.BlockingThreshold,
		})
	}

	return cfg, nil
}
