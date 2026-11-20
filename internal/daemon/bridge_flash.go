package daemon

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"freedo/internal/buildinfo"
)

type bridgeFlashStatus struct {
	State         string `json:"state"`
	Message       string `json:"message,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	FinishedAt    string `json:"finishedAt,omitempty"`
	Output        string `json:"output,omitempty"`
	Port          string `json:"port,omitempty"`
	ExpectedBuild string `json:"expectedBuild"`
}

func (d *daemon) getBridgeFlash(w http.ResponseWriter, r *http.Request) {
	d.bridgeFlashMu.RLock()
	status := d.bridgeFlash
	d.bridgeFlashMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"flash": status})
}

func (d *daemon) postBridgeFlash(w http.ResponseWriter, r *http.Request) {
	d.bridgeFlashMu.Lock()
	if d.bridgeFlash.State == "pausing" || d.bridgeFlash.State == "flashing" || d.bridgeFlash.State == "restarting" {
		d.bridgeFlashMu.Unlock()
		writeErr(w, http.StatusConflict, "a bridge firmware flash is already in progress")
		return
	}
	d.bridgeFlashMu.Unlock()

	if d.mode != "live" || d.bridgeFlashPort == "" {
		writeErr(w, http.StatusConflict, "a live serial bridge is required")
		return
	}

	image := d.bridgeFlashImage
	if image == "" {
		image = filepath.Join("/usr/share/freedo/bridge", "freedo-cc1101-bridge-"+strings.TrimPrefix(buildinfo.Version, "v")+"-merged.bin")
	}
	if _, err := os.Stat(image); err != nil {
		status := bridgeFlashStatus{
			State: "failed", Message: "bridge firmware image is missing: " + err.Error(),
			FinishedAt: time.Now().Format(time.RFC3339), Port: d.bridgeFlashPort,
			ExpectedBuild: strings.TrimPrefix(buildinfo.Version, "v"),
		}
		d.setBridgeFlash(status)
		writeErr(w, http.StatusConflict, status.Message)
		return
	}

	d.setBridgeFlash(bridgeFlashStatus{
		State: "pausing", Message: "closing the live radio",
		StartedAt: time.Now().Format(time.RFC3339), Port: d.bridgeFlashPort,
		ExpectedBuild: strings.TrimPrefix(buildinfo.Version, "v"),
	})

	go d.runBridgeFlash(image)
	writeJSON(w, http.StatusAccepted, map[string]any{"flash": d.currentBridgeFlash()})
}

func (d *daemon) currentBridgeFlash() bridgeFlashStatus {
	d.bridgeFlashMu.RLock()
	status := d.bridgeFlash
	d.bridgeFlashMu.RUnlock()
	return status
}

func (d *daemon) setBridgeFlash(status bridgeFlashStatus) {
	d.bridgeFlashMu.Lock()
	d.bridgeFlash = status
	d.bridgeFlashMu.Unlock()
}

func (d *daemon) runBridgeFlash(image string) {
	if err := d.radio.Close(); err != nil {
		d.setBridgeFlash(bridgeFlashStatus{
			State: "failed", Message: "could not close the live radio: " + err.Error(),
			FinishedAt: time.Now().Format(time.RFC3339), Port: d.bridgeFlashPort,
			ExpectedBuild: strings.TrimPrefix(buildinfo.Version, "v"),
		})
		return
	}

	current := d.currentBridgeFlash()
	current.State = "flashing"
	current.Message = "writing the bridge firmware"
	d.setBridgeFlash(current)

	esptool := d.bridgeFlashEsptool
	if esptool == "" {
		esptool = "esptool"
	}
	cmd := exec.Command(esptool,
		"--chip", "esp32",
		"--port", d.bridgeFlashPort,
		"--baud", "460800",
		"write_flash", "--erase-all", "0x0", image,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		current.State = "failed"
		current.Message = err.Error()
		current.FinishedAt = time.Now().Format(time.RFC3339)
		current.Output = strings.TrimSpace(output.String())
		d.setBridgeFlash(current)
		return
	}

	current.State = "restarting"
	current.Message = "firmware written; restarting the daemon"
	current.FinishedAt = time.Now().Format(time.RFC3339)
	current.Output = strings.TrimSpace(output.String())
	d.setBridgeFlash(current)

	select {
	case d.bridgeRestart <- struct{}{}:
	default:
	}
}
