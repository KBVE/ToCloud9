package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/walkline/ToCloud9/gen/matchmaking/pb"
)

// The debug middleware embeds pb.UnimplementedMatchmakingServiceServer, so an
// RPC it does not explicitly forward still satisfies the interface and quietly
// answers UNIMPLEMENTED at runtime. Adding JoinLFG to the proto without adding
// it here cost a live debugging session: the worldserver logged "method
// JoinLFG not implemented", fell back to creating dungeon groups locally, and
// the group service never learned they existed, so players could not leave.
//
// Every method is called on a middleware with no inner server. A forwarded
// method dereferences that nil interface and panics; the embedded stub returns
// an error instead. Comparing method pointers does not work here -- promoted
// methods do not compare reliably -- but this discriminates cleanly, and it
// covers RPCs added in the future rather than only the ones known today.
func TestDebugLoggerMiddlewareForwardsEveryRPC(t *testing.T) {
	middleware := reflect.ValueOf(NewMatchmakingDebugLoggerMiddleware(nil, zerolog.Nop()))
	serviceType := reflect.TypeOf((*pb.MatchmakingServiceServer)(nil)).Elem()

	for i := 0; i < serviceType.NumMethod(); i++ {
		method := serviceType.Method(i)

		if method.Type.NumIn() != 2 || method.Type.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
			continue // not a plain unary RPC
		}

		t.Run(method.Name, func(t *testing.T) {
			args := []reflect.Value{
				reflect.ValueOf(context.Background()),
				reflect.New(method.Type.In(1).Elem()),
			}

			forwarded := false
			func() {
				defer func() {
					if recover() != nil {
						forwarded = true
					}
				}()
				middleware.MethodByName(method.Name).Call(args)
			}()

			if !forwarded {
				t.Errorf("%s is not forwarded: it resolves to the embedded "+
					"UnimplementedMatchmakingServiceServer and will answer UNIMPLEMENTED "+
					"whenever debug logging is on", method.Name)
			}
		})
	}
}
