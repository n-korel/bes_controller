//go:build integration
// +build integration

package sipgo

import (
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationClientViaBindHost(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	{
		ua, _ := NewUA()
		defer ua.Close()
		srv, err := NewServer(ua)
		require.NoError(t, err)

		startTestServer(ctx, srv, "127.0.0.1:15099")
		srv.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
			res := sip.NewResponseFromRequest(req, 200, "OK", nil)
			tx.Respond(res)
		})
	}

	ua, _ := NewUA()
	defer ua.Close()
	client, err := NewClient(ua,
		WithClientHostname("127.0.0.1"),
		WithClientPort(15090),
		WithClientConnectionAddr("127.0.0.1:16099"),
	)
	require.NoError(t, err)

	options := sip.NewRequest(sip.OPTIONS, sip.Uri{User: "test", Host: "localhost"})
	tx, err := client.TransactionRequest(context.TODO(), options)
	require.NoError(t, err)

	clientTx := tx.(*sip.ClientTx)
	conn := clientTx.Connection()

	laddr := conn.LocalAddr()
	assert.Equal(t, "127.0.0.1:16099", laddr.String())

	via := options.Via()
	assert.Equal(t, "127.0.0.1", via.Host)
	assert.Equal(t, 15090, via.Port)
}

func TestIntegrationClientParalelDialing(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") == "" {
		t.Skip("Use TEST_INTEGRATION env value to run this test")
		return
	}

	ua, err := NewUA()
	require.NoError(t, err)
	defer ua.Close()

	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	defer l.Close()
	go func() {
		io.ReadAll(l)
	}()
	_, dstPort, err := sip.ParseAddr(l.LocalAddr().String())
	require.NoError(t, err)

	c, err := NewClient(ua,
		WithClientHostname("10.0.0.0"),
		WithClientConnectionAddr("127.0.0.1:15066"),
	)
	require.NoError(t, err)
	wg := sync.WaitGroup{}
	defer t.Log("Exiting")
	for i := 0; i < 2*runtime.NumCPU(); i++ {
		wg.Add(1)
		t.Log("Running", i)
		go func() {
			defer wg.Done()
			req := sip.NewRequest(sip.INVITE, sip.Uri{Host: "127.0.0.1", Port: dstPort})
			err := c.WriteRequest(req)
			require.NoError(t, err)
		}()
	}

	wg.Wait()

	// Check that connection reference count
	conn, err := ua.TransportLayer().GetConnection("udp", "127.0.0.1:15066")
	require.NoError(t, err)
	assert.Equal(t, 3, conn.Ref(0))
}

