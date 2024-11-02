package main

import (
	"context"
	"viam-filtered-camera-selective-sync/timeselectcamera"
	"viam-filtered-camera-selective-sync/timesyncsensor"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/utils"
)

func main() {
	// NewLoggerFromArgs will create a logging.Logger at "DebugLevel" if
	// "--log-level=debug" is an argument in os.Args and at "InfoLevel" otherwise.
	utils.ContextualMain(mainWithArgs, module.NewLoggerFromArgs("My Go Time Data Capture Module"))
}

func mainWithArgs(ctx context.Context, args []string, logger logging.Logger) (err error) {
	// instantiates the module itself
	myMod, err := module.NewModuleFromArgs(ctx)
	if err != nil {
		return err
	}

	// Register the camera model
	err = myMod.AddModelFromRegistry(ctx, camera.API, timeselectcamera.Model)
	if err != nil {
		return err
	}

	// Register the sensor model
	err = myMod.AddModelFromRegistry(ctx, sensor.API, timesyncsensor.Model)
	if err != nil {
		return err
	}

	// Each module runs as its own process
	err = myMod.Start(ctx)
	defer myMod.Close(ctx)
	if err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}
