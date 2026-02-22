package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/titagaki/peercast-pcp/pcp"
)

const agentName = "pcp-dump/1.0"

type config struct {
	addr      string
	channelID pcp.GnuID
	outPath   string
}

func main() {
	cfg, err := parseArgs(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintf(os.Stderr, "Usage: %s <host:port> <channel-id-hex> <output-file>\n", os.Args[0])
		os.Exit(1)
	}

	conn, err := handshake(cfg)
	if err != nil {
		log.Fatalf("[handshake] %v", err)
	}
	defer conn.Close()

	outFile, err := os.OpenFile(cfg.outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("failed to open output file: %v", err)
	}
	defer outFile.Close()
	log.Printf("writing stream data to %s", cfg.outPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	doneCh := make(chan struct{})
	go func() {
		<-sigCh
		log.Println("signal received, shutting down...")
		close(doneCh)
		conn.Close()
	}()

	runReceiveLoop(conn, outFile, doneCh)
}

// parseArgs はコマンドライン引数を検証し、config を返す。
func parseArgs(args []string) (config, error) {
	if len(args) != 4 {
		return config{}, fmt.Errorf("wrong number of arguments")
	}
	addr := args[1]
	chanIDHex := args[2]
	outPath := args[3]

	if len(chanIDHex) != 32 {
		return config{}, fmt.Errorf("channel ID must be a 32-character hex string, got %d characters", len(chanIDHex))
	}
	chanIDBytes, err := hex.DecodeString(chanIDHex)
	if err != nil {
		return config{}, fmt.Errorf("invalid channel ID hex: %w", err)
	}
	var channelID pcp.GnuID
	copy(channelID[:], chanIDBytes)

	return config{addr: addr, channelID: channelID, outPath: outPath}, nil
}

// handshake は PeerCast ノードへ接続し、PCP の helo/get ハンドシェイクを行う。
func handshake(cfg config) (*pcp.Conn, error) {
	log.Printf("[connect] dialing %s", cfg.addr)
	conn, err := pcp.Dial(cfg.addr)
	if err != nil {
		return nil, fmt.Errorf("dial failed: %w", err)
	}
	log.Printf("[connect] established; sent PCPConnect magic atom (pcp\\n)")

	heloAtom := pcp.NewParentAtom(
		pcp.PCPHelo,
		pcp.NewStringAtom(pcp.PCPHeloAgent, agentName),
		pcp.NewIDAtom(pcp.PCPHeloBCID, cfg.channelID),
	)
	log.Printf("[send] >>")
	logAtom(heloAtom, "       ")
	if err := conn.WriteAtom(heloAtom); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send helo: %w", err)
	}

	getAtom := pcp.NewParentAtom(
		pcp.PCPGet,
		pcp.NewIDAtom(pcp.PCPGetID, cfg.channelID),
	)
	log.Printf("[send] >>")
	logAtom(getAtom, "       ")
	if err := conn.WriteAtom(getAtom); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send get: %w", err)
	}

	return conn, nil
}

// runReceiveLoop は conn からアトムを読み続け、ストリームデータを outFile に書き込む。
// doneCh が閉じられるか、接続が切断されると終了する。
func runReceiveLoop(conn *pcp.Conn, outFile *os.File, doneCh <-chan struct{}) {
	var totalBytes int64
	lastReport := time.Now()

	for {
		select {
		case <-doneCh:
			log.Printf("total bytes written: %d", totalBytes)
			return
		default:
		}

		packet, err := conn.ReadAtom()
		if err != nil {
			select {
			case <-doneCh:
				log.Printf("[recv] connection closed by shutdown; total bytes written: %d", totalBytes)
				return
			default:
				if errors.Is(err, io.EOF) {
					log.Printf("[recv] EOF: peer closed connection; total bytes written: %d", totalBytes)
					return
				}
				log.Fatalf("[recv] read error: %v", err)
			}
		}

		log.Printf("[recv] <<")
		logAtom(packet, "       ")

		n, err := processPacket(packet, outFile)
		if err != nil {
			log.Fatalf("[write] error: %v", err)
		}
		totalBytes += int64(n)

		if n > 0 {
			log.Printf("[write] %d bytes appended (total: %d)", n, totalBytes)
		}
		if time.Since(lastReport) >= 5*time.Second {
			log.Printf("[progress] total bytes written: %d", totalBytes)
			lastReport = time.Now()
		}
	}
}




