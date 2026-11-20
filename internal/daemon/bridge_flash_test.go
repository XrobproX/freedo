package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"freedo/internal/radio"
	"freedo/tado"
)

func TestBridgeFlashRequiresLivePortAndImage(t *testing.T) {
	emu := tado.NewEmu(defaultInstall.EmuConfig(), tado.NewUnhandledLogger(io.Discard))
	d := newDaemon(defaultInstall, emu, radio.NewMock(nil), false, "live")
	d.bridgeFlashPort = ""
	d.bridgeFlashImage = "/definitely/missing/freedo-bridge.bin"

	ctx, cancel := context.WithCancel(context.Background())
	go d.run(ctx)
	t.Cleanup(cancel)
	srv := httptest.NewServer(d.mux())
	t.Cleanup(srv.Close)

	var response map[string]any
	code := postJSON(t, http.MethodPost, srv.URL+"/api/bridge/flash", nil, &response)
	if code != http.StatusConflict {
		t.Fatalf("POST flash status=%d, want 409", code)
	}

	d.bridgeFlashPort = "/dev/ttyUSB0"
	response = nil
	code = postJSON(t, http.MethodPost, srv.URL+"/api/bridge/flash", nil, &response)
	if code != http.StatusConflict {
		t.Fatalf("POST flash with missing image status=%d, want 409", code)
	}

	status := d.currentBridgeFlash()
	if status.State != "failed" {
		t.Fatalf("flash status=%+v, want failed", status)
	}
}
