package profiling

import (
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/sirupsen/logrus"
)

// Initializes CPU profiling and returns a function to stop profiling.
func InitCPUProfiling(cpuProfile *string) func() {
	logrus.Info("initializing CPU profiling")

	file, err := os.Create(*cpuProfile)
	if err != nil {
		logrus.WithError(err).Fatal("could not create CPU profile")
	}

	if err := pprof.StartCPUProfile(file); err != nil {
		logrus.WithError(err).Fatal("could not start CPU profile")
	}

	return func() {
		pprof.StopCPUProfile()

		if err := file.Close(); err != nil {
			logrus.WithError(err).Fatal("could not close CPU profile")
		}
	}
}

// Initializes memory profiling and returns a function to stop profiling.
func InitMemoryProfiling(memProfile *string) func() {
	logrus.Info("initializing memory profiling")

	return func() {
		file, err := os.Create(*memProfile)
		if err != nil {
			logrus.WithError(err).Fatal("could not create memory profile")
		}

		runtime.GC()

		if err := pprof.WriteHeapProfile(file); err != nil {
			logrus.WithError(err).Fatal("could not write memory profile")
		}

		if err = file.Close(); err != nil {
			logrus.WithError(err).Fatal("could not close memory profile")
		}
	}
}
