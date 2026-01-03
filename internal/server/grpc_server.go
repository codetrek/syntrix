package server

import (
	"fmt"
	"net"
)

func (s *serverImpl) runGRPCServer(errChan chan<- error) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.GRPCPort))
	if err != nil {
		errChan <- fmt.Errorf("grpc listen error: %w", err)
		return
	}
	s.logger.Info("Starting gRPC server", "port", s.cfg.GRPCPort)
	if err := s.grpcServer.Serve(lis); err != nil {
		errChan <- fmt.Errorf("grpc server error: %w", err)
	}
}
