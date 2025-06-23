package timeselectcamera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/utils"
)

// ScheduleHours defines start/end times for a weekday (HH:MM:SS)
type ScheduleHours struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// DateRange defines an explicit start and end timestamp (RFC3339)
type DateRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Config holds configuration for time-select-capture camera
// At least one of start_hours/end_hours, weekly_schedule, or schedule must be provided.
type Config struct {
	Camera string `json:"camera"`
	// Daily hours mode (HH:MM)
	StartHours string `json:"start_hours,omitempty"`
	EndHours   string `json:"end_hours,omitempty"`
	// Weekly schedule mode (map of weekday to ScheduleHours)
	WeeklySchedule map[string]ScheduleHours `json:"weekly_schedule,omitempty"`
	// Explicit date ranges mode
	Schedule []DateRange `json:"schedule,omitempty"`
}

// Validate ensures the configuration is correct and returns the camera dependency
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}

	hours := cfg.StartHours != "" && cfg.EndHours != ""
	weekly := len(cfg.WeeklySchedule) > 0
	dates := len(cfg.Schedule) > 0

	if !(hours || weekly || dates) {
		return nil, fmt.Errorf("%s: must specify at least one of start_hours/end_hours, weekly_schedule, or schedule", path)
	}

	if hours {
		if _, err := time.Parse("15:04", cfg.StartHours); err != nil {
			return nil, fmt.Errorf("%s: invalid start_hours %q: %w", path, cfg.StartHours, err)
		}
		if _, err := time.Parse("15:04", cfg.EndHours); err != nil {
			return nil, fmt.Errorf("%s: invalid end_hours %q: %w", path, cfg.EndHours, err)
		}
	}

	if weekly {
		days := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
		for _, day := range days {
			sh, ok := cfg.WeeklySchedule[day]
			if !ok {
				return nil, fmt.Errorf("%s: missing weekly_schedule for %s", path, day)
			}
			if sh.Start == "" || sh.End == "" {
				return nil, fmt.Errorf("%s: weekly_schedule[%s] must have start and end", path, day)
			}
			if _, err := time.Parse("15:04:05", sh.Start); err != nil {
				return nil, fmt.Errorf("%s: invalid weekly_schedule[%s].Start %q: %w", path, day, sh.Start, err)
			}
			if _, err := time.Parse("15:04:05", sh.End); err != nil {
				return nil, fmt.Errorf("%s: invalid weekly_schedule[%s].End %q: %w", path, day, sh.End, err)
			}
		}
	}

	if dates {
		for i, dr := range cfg.Schedule {
			start, err := time.Parse(time.RFC3339, dr.Start)
			if err != nil {
				return nil, fmt.Errorf("%s: schedule[%d].start invalid timestamp %q: %w", path, i, dr.Start, err)
			}
			end, err := time.Parse(time.RFC3339, dr.End)
			if err != nil {
				return nil, fmt.Errorf("%s: schedule[%d].end invalid timestamp %q: %w", path, i, dr.End, err)
			}
			if !start.Before(end) {
				return nil, fmt.Errorf("%s: schedule[%d] start must be before end", path, i)
			}
		}
	}

	return []string{cfg.Camera}, nil
}

var (
	// ErrNoCapture indicates skipping capture outside window
	ErrNoCapture = data.ErrNoCaptureToStore
	// ErrUnimplemented signals unimplemented optional methods
	ErrUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterComponent(camera.API, Model, resource.Registration[camera.Camera, *Config]{Constructor: newTimedCamera})
}

// Model identifies this camera
var Model = resource.NewModel("viam", "camera", "time-select-capture")

type timedCamera struct {
	name   resource.Name
	logger logging.Logger
	cfg    *Config
	inner  camera.Camera
	cancel func()
}

// newTimedCamera constructs and validates a timedCamera
func newTimedCamera(
	ctx context.Context,
	deps resource.Dependencies,
	rawConf resource.Config,
	logger logging.Logger,
) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	if _, err := conf.Validate(rawConf.ResourceName().String()); err != nil {
		return nil, err
	}

	innerCam, err := camera.FromDependencies(deps, conf.Camera)
	if err != nil {
		return nil, fmt.Errorf("%s: failed to resolve camera: %w", rawConf.ResourceName(), err)
	}

	_, cancel := context.WithCancel(ctx)
	return &timedCamera{
		name:   rawConf.ResourceName(),
		logger: logger,
		cfg:    conf,
		inner:  innerCam,
		cancel: cancel,
	}, nil
}

func (c *timedCamera) Reconfigure(
	ctx context.Context,
	deps resource.Dependencies,
	rawConf resource.Config,
) error {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		c.logger.Warnf("%s: reconfigure parse error: %v", rawConf.ResourceName(), err)
		return err
	}
	if _, err := conf.Validate(rawConf.ResourceName().String()); err != nil {
		return err
	}

	c.cfg = conf
	c.name = rawConf.ResourceName()
	c.inner, err = camera.FromDependencies(deps, conf.Camera)
	return err
}

// Image implements the gating logic around the inner camera
func (c *timedCamera) Image(
	ctx context.Context,
	mimeType string,
	extra map[string]interface{},
) ([]byte, camera.ImageMetadata, error) {
	if extra != nil && extra["fromDataManagement"] == true {
		now := time.Now()
		if !c.inWindow(now) {
			c.logger.Infof("%s: time %v outside window, skipping", c.name, now)
			return nil, camera.ImageMetadata{}, ErrNoCapture
		}
	}
	return c.inner.Image(ctx, mimeType, extra)
}

// inWindow checks if t is within any configured window
func (c *timedCamera) inWindow(t time.Time) bool {
	// Hours mode
	if c.cfg.StartHours != "" && c.cfg.EndHours != "" {
		sh, _ := time.Parse("15:04", c.cfg.StartHours)
		eh, _ := time.Parse("15:04", c.cfg.EndHours)
		start := time.Date(t.Year(), t.Month(), t.Day(), sh.Hour(), sh.Minute(), 0, 0, t.Location())
		end := time.Date(t.Year(), t.Month(), t.Day(), eh.Hour(), eh.Minute(), 0, 0, t.Location())
		if start.After(end) {
			end = end.Add(24 * time.Hour)
		}
		return !t.Before(start) && !t.After(end)
	}

	// Weekly schedule mode
	if len(c.cfg.WeeklySchedule) > 0 {
		day := strings.ToLower(t.Weekday().String()[:3])
		if sh, ok := c.cfg.WeeklySchedule[day]; ok {
			ts, _ := time.Parse("15:04:05", sh.Start)
			te, _ := time.Parse("15:04:05", sh.End)
			start := time.Date(t.Year(), t.Month(), t.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, t.Location())
			end := time.Date(t.Year(), t.Month(), t.Day(), te.Hour(), te.Minute(), te.Second(), 0, t.Location())
			if start.After(end) {
				end = end.Add(24 * time.Hour)
			}
			return !t.Before(start) && !t.After(end)
		}
	}

	// Explicit date ranges
	for _, dr := range c.cfg.Schedule {
		start, _ := time.Parse(time.RFC3339, dr.Start)
		end, _ := time.Parse(time.RFC3339, dr.End)
		if !t.Before(start) && !t.After(end) {
			return true
		}
	}

	return false
}

// Images is unimplemented
func (c *timedCamera) Images(ctx context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, ErrUnimplemented
}

// NextPointCloud is unimplemented
func (c *timedCamera) NextPointCloud(ctx context.Context) (pointcloud.PointCloud, error) {
	return nil, ErrUnimplemented
}

// DoCommand is unimplemented
func (c *timedCamera) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, ErrUnimplemented
}

// Close releases resources
func (c *timedCamera) Close(ctx context.Context) error {
	c.cancel()
	return nil
}

// Name returns the resource name
func (c *timedCamera) Name() resource.Name {
	return c.name
}

// Properties proxies to inner camera and disables PCD
func (c *timedCamera) Properties(ctx context.Context) (camera.Properties, error) {
	p, err := c.inner.Properties(ctx)
	if err == nil {
		p.SupportsPCD = false
	}
	return p, err
}
