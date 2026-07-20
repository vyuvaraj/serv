package pb

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string {
	return "json"
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

type MeshRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

type MeshResponse struct {
	StatusCode int32             `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

type MeshServiceClient interface {
	Forward(ctx context.Context, in *MeshRequest, opts ...grpc.CallOption) (*MeshResponse, error)
}

type meshServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewMeshServiceClient(cc grpc.ClientConnInterface) MeshServiceClient {
	return &meshServiceClient{cc}
}

func (c *meshServiceClient) Forward(ctx context.Context, in *MeshRequest, opts ...grpc.CallOption) (*MeshResponse, error) {
	out := new(MeshResponse)
	opts = append(opts, grpc.ForceCodec(jsonCodec{}))
	err := c.cc.Invoke(ctx, "/mesh.MeshService/Forward", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type MeshServiceServer interface {
	Forward(context.Context, *MeshRequest) (*MeshResponse, error)
}

func RegisterMeshServiceServer(s grpc.ServiceRegistrar, srv MeshServiceServer) {
	s.RegisterService(&MeshService_ServiceDesc, srv)
}

var MeshService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "mesh.MeshService",
	HandlerType: (*MeshServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Forward",
			Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
				in := new(MeshRequest)
				if err := dec(in); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(MeshServiceServer).Forward(ctx, in)
				}
				info := &grpc.UnaryServerInfo{
					Server:     srv,
					FullMethod: "/mesh.MeshService/Forward",
				}
				handler := func(ctx context.Context, req interface{}) (interface{}, error) {
					return srv.(MeshServiceServer).Forward(ctx, req.(*MeshRequest))
				}
				return interceptor(ctx, in, info, handler)
			},
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "mesh.proto",
}
