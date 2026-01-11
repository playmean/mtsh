package mt

import (
	"log/slog"
	"os"

	"github.com/exepirit/meshtastic-go/pkg/meshtastic/ble"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/mqtt"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/udp"
)

type slogAdapter struct {
	logger *slog.Logger
}

func (a slogAdapter) Debug(msg string, args ...any) {
	a.logger.Debug(msg, args...)
}

func (a slogAdapter) Info(msg string, args ...any) {
	a.logger.Info(msg, args...)
}

func (a slogAdapter) Warn(msg string, args ...any) {
	a.logger.Warn(msg, args...)
}

func (a slogAdapter) Error(msg string, args ...any) {
	a.logger.Error(msg, args...)
}

// EnableMeshtasticDebug turns on meshtastic-go debug logging for supported transports.
func EnableMeshtasticDebug() {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	adapter := slogAdapter{logger: logger}
	ble.Logger = adapter
	udp.Logger = adapter
	mqtt.Logger = adapter
}
