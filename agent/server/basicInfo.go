package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	monitoring "github.com/Aone2233/nekomari/agent/monitoring/unit"
	"github.com/Aone2233/nekomari/agent/protocol/transport"
	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/update"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
)

var flags = pkg_flags.GlobalConfig

func DoUploadBasicInfoWorks() {
	ticker := time.NewTicker(time.Duration(flags.InfoReportInterval) * time.Minute)
	for range ticker.C {
		err := uploadBasicInfo()
		if err != nil {
			log.Println("Error uploading basic info:", err)
		}
	}
}
func UpdateBasicInfo() {
	err := uploadBasicInfo()
	if err != nil {
		log.Println("Error uploading basic info:", err)
	} else {
		log.Println("Basic info uploaded successfully")
	}
}
func uploadBasicInfo() error {
	cpu := monitoring.CpuStaticInfo()

	osname := monitoring.OSName()
	kernelVersion := monitoring.KernelVersion()
	ipv4, ipv6, _ := monitoring.GetIPAddress()

	data := map[string]interface{}{
		"cpu_name":           cpu.CPUName,
		"cpu_cores":          cpu.CPUCores,
		"cpu_physical_cores": cpu.CPUPhysicalCores,
		"arch":               cpu.CPUArchitecture,
		"os":                 osname,
		"kernel_version":     kernelVersion,
		"ipv4":               ipv4,
		"ipv6":               ipv6,
		"mem_total":          monitoring.Ram().Total,
		"swap_total":         monitoring.Swap().Total,
		"disk_total":         monitoring.Disk().Total,
		"gpu_name":           monitoring.GpuName(),
		"virtualization":     monitoring.Virtualized(),
		"version":            update.CurrentVersion,
		// What this host can do about ICMP, so the panel can tell "this node
		// cannot probe" apart from "the target did not answer" (roadmap E2). The
		// payload is a free-form map, so this is additive in both directions: an
		// older panel ignores the key, and an older agent simply does not send it
		// — which the panel renders as "unknown" rather than "unavailable".
		"icmp_capability": ICMPCapability(),
	}

	return tryUploadData(data)
}

func tryUploadData(data map[string]interface{}) error {
	return tryUploadDataWithProtocol(data)
}

func tryUploadDataWithProtocol(data map[string]interface{}) error {
	endpoint := strings.TrimSuffix(flags.Endpoint, "/") + "/api/clients/v2/rpc?token=" + flags.Token
	payload := v2.BuildBasicInfoPayload(data)
	body := payload
	compressed := false
	if !flags.DisableCompression {
		if gz, err := transport.GzipBytes(payload); err == nil {
			body = gz
			compressed = true
		}
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}

	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	message := string(respBody)

	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: message}
	}
	if len(bytes.TrimSpace(respBody)) > 0 {
		if _, err := parseV2Response(respBody); err != nil {
			return err
		}
	}

	return nil
}
