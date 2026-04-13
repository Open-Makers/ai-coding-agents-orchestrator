package cpulimit

import (
	"log/slog"
	"runtime"
	"strconv"
	"sync"
)

// minUsableCores is the minimum number of cores the orchestrator will use,
// even if reserved_cores would leave fewer.
const minUsableCores = 1

var (
	once          sync.Once
	allowedCores  int
	reservedCores int
)

// Apply sets GOMAXPROCS so that reservedN cores are left free for other
// processes. It is safe to call multiple times — only the first call takes
// effect. Returns the number of cores allocated to the orchestrator.
func Apply(reservedN int) int {
	once.Do(func() {
		total := runtime.NumCPU()
		reservedCores = reservedN
		if reservedCores < 0 {
			reservedCores = 0
		}

		allowedCores = total - reservedCores
		if allowedCores < minUsableCores {
			allowedCores = minUsableCores
		}

		prev := runtime.GOMAXPROCS(allowedCores)
		slog.Info("cpu limit applied",
			slog.Int("total_cores", total),
			slog.Int("reserved_cores", reservedCores),
			slog.Int("allowed_cores", allowedCores),
			slog.Int("prev_gomaxprocs", prev),
		)
	})
	return allowedCores
}

// AllowedCores returns the number of cores available after reservation.
// Must be called after Apply; returns runtime.NumCPU() if Apply was not called.
func AllowedCores() int {
	if allowedCores == 0 {
		return runtime.NumCPU()
	}
	return allowedCores
}

// EnvOverrides returns environment variable strings that should be prepended
// to child process env slices to enforce CPU limits.
func EnvOverrides() []string {
	cores := AllowedCores()
	return []string{
		"GOMAXPROCS=" + strconv.Itoa(cores),
		"OLLAMA_NUM_THREADS=" + strconv.Itoa(cores),
		"OMP_NUM_THREADS=" + strconv.Itoa(cores),
		"MKL_NUM_THREADS=" + strconv.Itoa(cores),
		"OPENBLAS_NUM_THREADS=" + strconv.Itoa(cores),
	}
}
