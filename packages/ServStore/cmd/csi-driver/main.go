package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
)

type CSIDriver struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedNodeServer
	csi.UnimplementedControllerServer
}

func (d *CSIDriver) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          "csi.servstore.io",
		VendorVersion: "1.0.0",
	}, nil
}

func (d *CSIDriver) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
		},
	}, nil
}

func (d *CSIDriver) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}

func (d *CSIDriver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *CSIDriver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *CSIDriver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	volumeID := req.GetVolumeId()
	
	// Create directory mount path
	if err := os.MkdirAll(targetPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Write simple text configuration file inside target path so S3 clients/pods running
	// in this node can read endpoint/creds or mounting configuration without starting a real FUSE filesystem.
	configPath := fmt.Sprintf("%s/servstore-csi.config", targetPath)
	configData := fmt.Sprintf("volume-id=%s\nendpoint=%s\nbucket=%s\n",
		volumeID,
		os.Getenv("SERVSTORE_ENDPOINT"),
		req.GetVolumeContext()["bucket"],
	)
	
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		return nil, fmt.Errorf("failed to write csi configuration: %w", err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *CSIDriver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	
	// Clean up config file and directory mount path
	_ = os.Remove(fmt.Sprintf("%s/servstore-csi.config", targetPath))
	_ = os.Remove(targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *CSIDriver) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (d *CSIDriver) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	nodeID := os.Getenv("NODE_NAME")
	if nodeID == "" {
		nodeID = "servstore-node"
	}
	return &csi.NodeGetInfoResponse{
		NodeId: nodeID,
	}, nil
}

func main() {
	endpoint := os.Getenv("CSI_ENDPOINT")
	if endpoint == "" {
		endpoint = "unix:///var/lib/kubelet/plugins/csi.servstore.io/csi.sock"
	}

	fmt.Printf("Starting ServStore CSI Driver on endpoint: %s\n", endpoint)

	// Clean up existing socket files
	proto, addr, found := stringsCut(endpoint, "://")
	if !found {
		addr = endpoint
		proto = "unix"
	}
	if proto == "unix" {
		_ = os.Remove(addr)
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on address: %v\n", err)
		os.Exit(1)
	}

	server := grpc.NewServer()
	driver := &CSIDriver{}

	csi.RegisterIdentityServer(server, driver)
	csi.RegisterNodeServer(server, driver)
	csi.RegisterControllerServer(server, driver)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.Serve(listener); err != nil {
			fmt.Fprintf(os.Stderr, "failed to serve gRPC: %v\n", err)
			os.Exit(1)
		}
	}()

	<-stop
	server.GracefulStop()
	fmt.Println("ServStore CSI Driver stopped gracefully")
}

// Simple fallback cut for strings since strings.Cut was introduced in newer Go versions
func stringsCut(s, sep string) (before, after string, found bool) {
	if i := indexOf(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func indexOf(s, sep string) int {
	n := len(sep)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sep {
			return i
		}
	}
	return -1
}
