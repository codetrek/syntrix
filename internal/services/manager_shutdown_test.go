package services

import (
	"context"
	"testing"
	"time"

	"syntrix/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestManager_Init_Start_Shutdown_NoServices(t *testing.T) {
	cfg := config.LoadConfig()
	opts := Options{}
	mgr := NewManager(cfg, opts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assert.NoError(t, mgr.Init(ctx))

	bgCtx, bgCancel := context.WithCancel(context.Background())
	mgr.Start(bgCtx)
	bgCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	mgr.Shutdown(shutdownCtx)
}
