package client

import (
	"context"
	"github.com/makeitplay/client-player-go/lugo"
	"github.com/makeitplay/client-player-go/proto"
	"google.golang.org/grpc"
	"io"
)

const ProtocolVersion = "2.0"

func NewClient(config Config) (context.Context, lugo.Client, error) {
	var err error
	c := &client{}

	if config.Insecure {
		c.grpcConn, err = grpc.Dial(config.GRPCAddress, grpc.WithInsecure())
	} else {
		c.grpcConn, err = grpc.Dial(config.GRPCAddress)
	}
	if err != nil {
		return nil, nil, err
	}

	c.gameConn = proto.NewGameClient(c.grpcConn)

	c.senderBuilder = func(snapshot *proto.GameSnapshot, logger lugo.Logger) lugo.OrderSender {
		return &sender{
			gameConn: c.gameConn,
			snapshot: snapshot,
			logger:   logger,
		}
	}

	c.ctx, c.stopCtx = context.WithCancel(context.Background())
	if c.stream, err = c.gameConn.JoinATeam(c.ctx, &proto.JoinRequest{
		Token:           config.Token,
		Number:          config.Number,
		InitPosition:    &config.InitialPosition,
		TeamSide:        config.TeamSide,
		ProtocolVersion: ProtocolVersion,
	}); err != nil {
		return nil, nil, err
	}
	return c.ctx, c, nil
}

type client struct {
	stream        proto.Game_JoinATeamClient
	gameConn      proto.GameClient
	grpcConn      *grpc.ClientConn
	ctx           context.Context
	stopCtx       context.CancelFunc
	senderBuilder func(snapshot *proto.GameSnapshot, logger lugo.Logger) lugo.OrderSender
	sender        lugo.OrderSender
}

func (c client) OnNewTurn(decider lugo.DecisionMaker, log lugo.Logger) {
	go func() {
		for {
			snapshot, err := c.stream.Recv()
			if err != nil {
				if err == io.EOF {
					log.Infof("gRPC connection closed")
				} else {
					log.Errorf("gRPC stream error: %s", err)
				}
				c.stopCtx()
				return
			}
			log.Debugf("calling DecisionMaker for turn %d", snapshot.Turn)
			decider(snapshot, c.senderBuilder(snapshot, log))
		}
	}()
}

func (c client) Stop() error {
	c.stopCtx()
	return c.grpcConn.Close()
}

func (c client) GetGRPCConn() *grpc.ClientConn {
	return c.grpcConn
}

func (c client) GetServiceConn() proto.GameClient {
	return c.gameConn
}

func (c client) SenderBuilder(builder func(snapshot *proto.GameSnapshot, logger lugo.Logger) lugo.OrderSender) {
	c.senderBuilder = builder
}

type sender struct {
	snapshot *proto.GameSnapshot
	logger   lugo.Logger
	gameConn proto.GameClient
}

func (s sender) Send(ctx context.Context, orders []proto.PlayerOrder, debugMsg string) (*proto.OrderResponse, error) {
	orderSet := &proto.OrderSet{
		Turn:         s.snapshot.Turn,
		DebugMessage: debugMsg,
		Orders:       []*proto.Order{},
	}
	for _, order := range orders {
		orderSet.Orders = append(orderSet.Orders, &proto.Order{Action: order})
	}
	s.logger.Debugf("sending orders for turn %d", s.snapshot.Turn)
	return s.gameConn.SendOrders(ctx, orderSet)
}
