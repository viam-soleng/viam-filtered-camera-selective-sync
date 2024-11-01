package timesyncsensor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// Init called upon import, registers this component with the module
func init() {
	resource.RegisterComponent(sensor.API, Model, resource.Registration[sensor.Sensor, *Config]{Constructor: newtimeSensor})
}

// Error for unimplemented functions
var errUnimplemented = errors.New("unimplemented")

// Model defines the sensor model's identifier
var Model = resource.NewModel("viam", "sensor", "time-select-sync")

// Config holds the sensor and time configuration
type Config struct {
	StartHours string `json:"start_hours"`
	EndHours   string `json:"end_hours"`
}

// timeSensor represents the custom sensor struct
type timeSensor struct {
	name       resource.Name
	logger     logging.Logger
	cfg        *Config
	cancelCtx  context.Context
	cancelFunc func()
}

// Validate configuration and return implicit dependencies
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.StartHours == "" || cfg.EndHours == "" {
		return nil, fmt.Errorf("start_hours and end_hours are required for component %q", path)
	}

	// Validate time format
	if _, err := time.Parse("15:04", cfg.StartHours); err != nil {
		return nil, fmt.Errorf("invalid start_hours format (HH:MM) for component %q", path)
	}
	if _, err := time.Parse("15:04", cfg.EndHours); err != nil {
		return nil, fmt.Errorf("invalid end_hours format (HH:MM) for component %q", path)
	}

	return []string{}, nil
}

// Constructor for timeSensor
func newtimeSensor(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (sensor.Sensor, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	cancelCtx, cancelFunc := context.WithCancel(ctx)

	return &timeSensor{
		name:       rawConf.ResourceName(),
		logger:     logger,
		cfg:        conf,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}, nil
}

// Return name of sensor
func (c *timeSensor) Name() resource.Name {
	return c.name
}

// Reconfigure updates the model with new dependencies and configuration
func (s *timeSensor) Reconfigure(ctx context.Context, deps resource.Dependencies, conf resource.Config) error {
	newConfig, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		s.logger.Warnf("Error parsing new configuration: %v", err)
		return err
	}

	s.name = conf.ResourceName()
	s.cfg = newConfig // Apply new configuration to struct
	return nil
}

// Readings returns "true" if within the configured hours, otherwise "false"
func (s *timeSensor) Readings(ctx context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
	currentTime := time.Now()
	startTime, err := time.Parse("15:04", s.cfg.StartHours)
	if err != nil {
		s.logger.Errorf("Invalid start time format: %v", err)
		return nil, err
	}
	endTime, err := time.Parse("15:04", s.cfg.EndHours)
	if err != nil {
		s.logger.Errorf("Invalid end time format: %v", err)
		return nil, err
	}

	// Adjust start and end times to today's date for comparison
	startTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), startTime.Hour(), startTime.Minute(), 0, 0, currentTime.Location())
	endTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), endTime.Hour(), endTime.Minute(), 0, 0, currentTime.Location())

	// Check if current time is within the configured start and end hours
	withinTimeRange := currentTime.After(startTime) && currentTime.Before(endTime)
	return map[string]interface{}{
		"within_time_range": withinTimeRange,
	}, nil
}

// DoCommand can be implemented to extend sensor functionality but returns unimplemented in this example.
func (s *timeSensor) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, errUnimplemented
}

// Close cleans up the sensor
func (s *timeSensor) Close(ctx context.Context) error {
	s.cancelFunc()
	return nil
}
