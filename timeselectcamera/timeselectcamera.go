package timeselectcamera

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/data"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/utils"
)

// Init called upon import, registers this component with the module
func init() {
	resource.RegisterComponent(camera.API, Model, resource.Registration[camera.Camera, *Config]{Constructor: newtimedCamera})
}

// Error for unimplemented functions
var errUnimplemented = errors.New("unimplemented")

// Model defines the camera model's identifier
var Model = resource.NewModel("viam", "camera", "time-select-capture")

// Config holds the camera and time configuration
type Config struct {
	Camera     string `json:"camera"`
	StartHours string `json:"start_hours"`
	EndHours   string `json:"end_hours"`
}

// timedCamera represents the custom camera struct
type timedCamera struct {
	name       resource.Name
	logger     logging.Logger
	cfg        *Config
	cam        camera.Camera
	cancelCtx  context.Context
	cancelFunc func()
}

// Validate configuration and return implicit dependencies
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}
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

	return []string{cfg.Camera}, nil
}

// Constructor for timedCamera
func newtimedCamera(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (camera.Camera, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	// Retrieve camera dependency
	cam, err := camera.FromDependencies(deps, conf.Camera)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve camera dependency %q: %w", conf.Camera, err)
	}

	cancelCtx, cancelFunc := context.WithCancel(ctx)

	return &timedCamera{
		name:       rawConf.ResourceName(),
		logger:     logger,
		cfg:        conf,
		cam:        cam,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}, nil
}

// Reconfigure updates the model with new dependencies and configuration
func (c *timedCamera) Reconfigure(ctx context.Context, deps resource.Dependencies, conf resource.Config) error {
	newConfig, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		c.logger.Warnf("Error parsing new configuration: %v", err)
		return err
	}

	c.name = conf.ResourceName()
	c.cfg = newConfig // Apply new configuration to struct

	// Retrieve updated camera dependency
	cam, err := camera.FromDependencies(deps, newConfig.Camera)
	if err != nil {
		c.logger.Errorf("Failed to retrieve updated camera dependency %q: %v", newConfig.Camera, err)
		return err
	}
	c.cam = cam // Apply updated camera dependency

	return nil
}

// Images does nothing.
func (c *timedCamera) Images(ctx context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errUnimplemented
}

// NextPointCloud returns the next PointCloud from the camera, or will error if not supported
func (c *timedCamera) NextPointCloud(ctx context.Context) (pointcloud.PointCloud, error) {
	c.logger.Error("NextPointCloud method unimplemented")
	return nil, errUnimplemented
}

func (c *timedCamera) Name() resource.Name {
	return c.name
}

// DoCommand extends the camera API with additional commands (optional)
func (c *timedCamera) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	c.logger.Error("DoCommand method unimplemented")
	return nil, errUnimplemented
}

// The camera image stream
func (c *timedCamera) Stream(ctx context.Context, errHandlers ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	cameraStream, err := c.cam.Stream(ctx, errHandlers...)
	if err != nil {
		return nil, err
	}
	return timedStream{cameraStream, c}, nil
}

type timedStream struct {
	cameraStream gostream.VideoStream
	c            *timedCamera
}

// Gets the next image from the image stream, capturing only within the configured time range if the request is from DataManager
func (ts timedStream) Next(ctx context.Context) (image.Image, func(), error) {
	// Retrieve extra data to check if the request is from DataManager
	extra, ok := camera.FromContext(ctx)
	if !ok {
		ts.c.logger.Debug("No extra data in context; proceeding without DataManager checks")
		// Proceed to get the next image without time checks
		return ts.cameraStream.Next(ctx)
	}

	ts.c.logger.Debug(extra != nil && extra["fromDataManagement"] == true)

	if extra != nil && extra["fromDataManagement"] == true {
		ts.c.logger.Debug("DataManager request")
		currentTime := time.Now()

		// Parse start and end times
		startTime, err := time.Parse("15:04", ts.c.cfg.StartHours)
		if err != nil {
			ts.c.logger.Errorf("Invalid start time format: %v", err)
			return nil, nil, err
		}
		endTime, err := time.Parse("15:04", ts.c.cfg.EndHours)
		if err != nil {
			ts.c.logger.Errorf("Invalid end time format: %v", err)
			return nil, nil, err
		}

		// Handle overnight period where start_time is later in the day than end_time
		overnight := false

		// Adjust start and end times to today's date for comparison
		startTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), startTime.Hour(), startTime.Minute(), 0, 0, currentTime.Location())
		endTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), endTime.Hour(), endTime.Minute(), 0, 0, currentTime.Location())

		// Handle overnight period where start_time is later in the day than end_time
		if startTime.After(endTime) {
			endTime = endTime.Add(24 * time.Hour) // Adjust endTime to the next day
			overnight = true
		}
		// Determine if current time falls within the specified range
		inRange := !currentTime.Before(startTime) && !currentTime.After(endTime)
		ts.c.logger.Debug("In range?", inRange)
		if !inRange {
			ts.c.logger.Info("Current time is outside capture hours (overnight: %v); skipping image capture", overnight)
			return nil, nil, data.ErrNoCaptureToStore
		}
	}

	// Get the next camera image if within the time range or if the request is not from DataManager
	img, release, err := ts.cameraStream.Next(ctx)
	if err != nil {
		ts.c.logger.Error("Current time is outside capture hours (overnight: %v); skipping image capture", err)
		return nil, nil, err
	}

	// Return the image if captured
	return img, release, nil
}

// Close closes the image stream with a context
func (ts timedStream) Close(ctx context.Context) error {
	return ts.cameraStream.Close(ctx)
}

// Close closes the camera and releases any associated resources
func (c *timedCamera) Close(ctx context.Context) error {
	c.cancelFunc()
	return nil
}

func (c *timedCamera) Properties(ctx context.Context) (camera.Properties, error) {
	p, err := c.cam.Properties(ctx)
	if err == nil {
		p.SupportsPCD = false
	}
	return p, err
}
