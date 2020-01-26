package main

import (
	"context"
	"flag"
	"fmt"
	discovery "github.com/libp2p/go-libp2p-discovery"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"time"

	// We need to import libp2p's libraries that we use in this project.
	"github.com/libp2p/go-libp2p"
	circuit "github.com/libp2p/go-libp2p-circuit"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/peerstore"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	ma "github.com/multiformats/go-multiaddr"
)

// Protocol defines the libp2p protocol that we will use for the libp2p proxy
// service that we are going to provide. This will tag the streams used for
// this service. Streams are multiplexed and their protocol tag helps
// libp2p handle them to the right handler functions.
const Protocol = "/mpcoord/0.0.1"
const Rendezvous = "/mpcoord"

// makeRandomHost creates a libp2p host with a randomly generated identity.
// This step is described in depth in other tutorials.
func makeRandomHost() (host.Host, *kaddht.IpfsDHT) {
	ctx := context.Background()
	port := 10000 + rand.Intn(10000)

	host, err := libp2p.New(ctx,
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)),
		libp2p.EnableRelay(circuit.OptHop, circuit.OptDiscovery))
	if err != nil {
		log.Fatal(err)
	}

	// Bootstrap the DHT. In the default configuration, this spawns a Background
	// thread that will refresh the peer table every five minutes.
	dht, err := kaddht.New(ctx, host)
	if err != nil {
		log.Fatal(err)
	}
	err = dht.Bootstrap(ctx)
	if err != nil {
		log.Fatal(err)
	}

	return host, dht
}

// ProxyService provides HTTP proxying on top of libp2p by launching an
// HTTP server which tunnels the requests to a destination peer running
// ProxyService too.
type ProxyService struct {
	host       host.Host
	remotePeer peer.ID
}

// NewProxyService attaches a proxy service to the given libp2p Host.
// The localListenAddr parameter specifies the address on which the
// HTTP proxy server listens. The remotePeer parameter specifies the peer
// ID of the remote peer in charge of performing the HTTP requests.
//
// ProxyAddr/remotePeer may be nil/"" it is not necessary that this host
// provides a listening HTTP server (and instead its only function is to
// perform the proxied http requests it receives from a different peer.
//
// The addresses for the remotePeer peer should be part of the host's peerstore.
func NewProxyService(h host.Host) *ProxyService {
	// We let our host know that it needs to handle streams tagged with the
	// protocol id that we have defined, and then handle them to
	// our own streamHandling function.
	h.SetStreamHandler(Protocol, func(stream network.Stream) {
		handleRemoteConnection(stream)
	})

	return &ProxyService{
		host: h,
	}
}

func startDiscovery(dht *kaddht.IpfsDHT) chan peer.AddrInfo {
	// Advertise our presence.
	routingDiscovery := discovery.NewRoutingDiscovery(dht)
	discovery.Advertise(context.Background(), routingDiscovery, Rendezvous)

	// Look for peers regularly.
	peerChan := make(chan peer.AddrInfo, 100)

	go func() {
		for {
			tmpPeerChan, err := routingDiscovery.FindPeers(context.Background(), Rendezvous)
			if err != nil {
				log.Fatal(err)
			}

			for peerInfo := range tmpPeerChan {
				if peerInfo.ID != dht.Host().ID() {
					peerChan <- peerInfo
				}
			}

			time.Sleep(time.Minute)
		}
	}()

	return peerChan
}

// handleRemoteConnection is our function to handle any libp2p-net streams that belong
// to our protocol. The streams should contain an HTTP request which we need
// to parse, make on behalf of the original node, and then write the response
// on the stream, before closing it.
func handleRemoteConnection(stream network.Stream) {
	log.Println("server: forwarding remote connection to local server")

	port := 20000 + rand.Intn(10000)
	go runExternal("incoming-connection", port)

	// Connect.
	for i := 0; i < 60; i++ {
		conn, err := net.Dial("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Println("Warning:", err, ", retrying…")
			time.Sleep(time.Second)
			continue
		}

		// Forward between stream and conn.
		go forward(stream, conn)
		go forward(conn, stream)
		return
	}
	log.Println("Error: could not reach local server.")
}

func (p *ProxyService) Serve(remotePeer peer.ID, port int) {
	log.Println("client: listening for local requests")
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Fatal(err)
			}
			go p.handleLocalConnection(conn, remotePeer)
		}
	}()
}

func (p *ProxyService) handleLocalConnection(conn net.Conn, remotePeer peer.ID) {
	log.Println("client: forwarding local connection to remote peer ", remotePeer)
	// We need to send the request to the remote libp2p peer, so
	// we open a stream to it
	stream, err := p.host.NewStream(context.Background(), remotePeer, Protocol)
	if err != nil {
		log.Println(err)
		return
	}

	// Forward between stream and conn.
	go forward(stream, conn)
	go forward(conn, stream)
}

func runExternal(event string, port int) {
	cmd := exec.Command("make", event)
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}
}

func forward(dst io.WriteCloser, src io.ReadCloser) {
	_, err := io.Copy(dst, src)
	if err != nil {
		log.Println(err)
	}
	dst.Close()
}

func parseAddress(addr string) *peer.AddrInfo {
	parsed, err := ma.NewMultiaddr(addr)
	if err != nil {
		log.Fatalln(err)
	}
	peerInfo, err := peer.AddrInfoFromP2pAddr(parsed)
	if err != nil {
		log.Fatal(err)
	}
	return peerInfo
}

func addRelayAddress(relayAddr string, peerInfo *peer.AddrInfo) {
	if relayAddr == "" {
		return
	}
	addr := fmt.Sprintf("%s/p2p-circuit/p2p/%s", relayAddr, peer.IDB58Encode(peerInfo.ID))
	maddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		log.Println("Warning:", err)
		return
	}
	peerInfo.Addrs = append(peerInfo.Addrs, maddr)
}

// addAddrToPeerstore parses a peer multiaddress and adds
// it to the given host's peerstore, so it knows how to
// contact it. It returns the peer ID of the remote peer.
func addAddrToPeerstore(h host.Host, addr string) *peer.AddrInfo {
	peerInfo := parseAddress(addr)
	// We have a peer ID and a targetAddr so we add
	// it to the peerstore so LibP2P knows how to contact it
	h.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, peerstore.PermanentAddrTTL)
	return peerInfo
}

func connectToPeer(h host.Host, addr string) (*peer.AddrInfo, error) {
	peerInfo := parseAddress(addr)
	err := h.Connect(context.Background(), *peerInfo)
	if err != nil {
		return nil, err
	}
	return peerInfo, nil
}

const help = `
This example creates a simple TCP Proxy using two libp2p peers. The first peer
provides an TCP server locally which tunnels the TCP requests with libp2p
to a remote peer. The remote peer performs the requests and 
send the sends the response back.

Usage: Start remote peer first with:   ./mpcoord
       Then start the local peer with: ./mpcoord -c <remote-peer-multiaddress>
`

func main() {
	rand.Seed(time.Now().UnixNano())

	flag.Usage = func() {
		log.Println(help)
		flag.PrintDefaults()
	}

	// Parse some flags
	remotePeer := flag.String("c", "", "remote peer address")
	pureRelay := flag.Bool("R", false, "run as a relay only")
	relayPeer := flag.String("r", "", "connect to this relay")
	flag.Parse()

	name := ""

	if *pureRelay {
		name = "relay"
	} else if *remotePeer != "" {
		name = "client"
	} else {
		name = "server"
	}

	log.SetFlags(log.Lshortfile)
	log.SetPrefix(name + ": ")

	host, dht := makeRandomHost()
	addr := ""

	log.Println("Node", host.ID())
	log.Println("libp2p-peer addresses:")
	for _, a := range host.Addrs() {
		addr = fmt.Sprintf("%s/p2p/%s", a, peer.IDB58Encode(host.ID()))
		fmt.Println(addr)
	}

	if *relayPeer != "" {
		log.Println("Connecting to relay", *relayPeer)
		connectToPeer(host, *relayPeer)

		addr = fmt.Sprintf("%s/p2p-circuit/p2p/%s", *relayPeer, peer.IDB58Encode(host.ID()))
		fmt.Println(addr)
	}

	// Save our address to a file.
	filename := "local/" + name + ".p2p"
	fd, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	_, err = io.WriteString(fd, addr)
	if err != nil {
		log.Fatal(err)
	}
	fd.Close()
	log.Println("Wrote my address in", filename)

	if *pureRelay {
		log.Println("Running as relay.")
		<-make(chan struct{}) // hang forever as relay
	} else {

		// Start the service.
		proxy := NewProxyService(host)
		peerChan := make(chan peer.AddrInfo, 1)

		if name == "client" {
			// Client mode: connect to the provided peer.
			remotePeerInfo := parseAddress(*remotePeer)
			peerChan <- *remotePeerInfo
			close(peerChan)
		} else {
			// Auto mode: discover peers.
			peerChan = startDiscovery(dht)
		}

		for peerInfo := range peerChan {
			log.Println("Found peer", peerInfo)
			addRelayAddress(*relayPeer, &peerInfo)

			// Make sure our host knows how to reach remotePeer.
			err := host.Connect(context.Background(), peerInfo)
			if err != nil {
				log.Println("Failed to connect, skipping")
				continue
			}

			port := 30000 + rand.Intn(10000)
			// Listen for local backend connections.
			proxy.Serve(peerInfo.ID, port)
			// The backend client will connect to the proxy.Serve above.
			runExternal("outgoing-connection", port)
		}
	}

}
