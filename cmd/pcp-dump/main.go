package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/titagaki/peercast-pcp/pcp"
)

const (
	agentName   = "pcp-dump/1.0"
	maxRedirect = 8
)

type config struct {
	addr      string
	channelID pcp.GnuID
	chanIDHex string
	outPath   string
}

func main() {
	cfg, err := parseArgs(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintf(os.Stderr, "使い方: %s <host:port> <channel-id-hex> <output-file>\n", os.Args[0])
		os.Exit(1)
	}

	outFile, err := os.OpenFile(cfg.outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("出力ファイルのオープン失敗: %v", err)
	}
	defer outFile.Close()
	log.Printf("ストリームデータを %s に書き込みます", cfg.outPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	doneCh := make(chan struct{})
	go func() {
		<-sigCh
		log.Println("シグナル受信、シャットダウンします...")
		close(doneCh)
	}()

	if err := connect(cfg, outFile, doneCh, 0); err != nil {
		log.Fatalf("[error] %v", err)
	}
}

// parseArgs はコマンドライン引数を検証し、config を返す。
func parseArgs(args []string) (config, error) {
	if len(args) != 4 {
		return config{}, errors.New("引数の数が正しくありません")
	}
	addr := args[1]
	chanIDHex := args[2]
	outPath := args[3]

	if len(chanIDHex) != 32 {
		return config{}, fmt.Errorf("チャンネルIDは32文字の16進数である必要があります: %q", chanIDHex)
	}
	chanIDBytes, err := hex.DecodeString(chanIDHex)
	if err != nil {
		return config{}, fmt.Errorf("チャンネルIDのパース失敗: %w", err)
	}
	var channelID pcp.GnuID
	copy(channelID[:], chanIDBytes)

	return config{
		addr:      addr,
		channelID: channelID,
		chanIDHex: chanIDHex,
		outPath:   outPath,
	}, nil
}

// connect は指定アドレスへ TCP 接続し、HTTP ハンドシェイク後にステータスに応じた処理を行う。
// 503 リダイレクト時は depth を増やして再帰的に接続する。
func connect(cfg config, outFile *os.File, doneCh <-chan struct{}, depth int) error {
	if depth > maxRedirect {
		return fmt.Errorf("リダイレクト回数が上限（%d）を超えました", maxRedirect)
	}

	log.Printf("[connect] %s に接続中 (試行 %d/%d)", cfg.addr, depth+1, maxRedirect+1)
	conn, err := net.Dial("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("TCP接続失敗: %w", err)
	}
	defer conn.Close()

	// シャットダウン時にコネクションを閉じるゴルーチン
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-doneCh:
			conn.Close()
		case <-stopCh:
		}
	}()

	// HTTP リクエスト送信
	req := fmt.Sprintf("GET /channel/%s HTTP/1.0\r\nx-peercast-pcp:1\r\n\r\n", cfg.chanIDHex)
	log.Printf("[http] リクエスト送信: GET /channel/%s HTTP/1.0", cfg.chanIDHex)
	if _, err := fmt.Fprint(conn, req); err != nil {
		return fmt.Errorf("HTTPリクエスト送信失敗: %w", err)
	}

	// HTTP レスポンスヘッダを手動で読み取る
	// （net/http.ReadResponse はボディを内部バッファに取り込むため生 bufio.Reader を使う）
	br := bufio.NewReader(conn)
	status, err := readHTTPResponse(br)
	if err != nil {
		return err
	}
	log.Printf("[http] ステータスコード: %d", status)

	switch status {
	case 404:
		return fmt.Errorf("チャンネルが見つかりません (404): %s", cfg.chanIDHex)
	case 503:
		return handle503(conn, br, cfg, outFile, doneCh, depth)
	case 200:
		return handle200(conn, br, cfg, outFile, doneCh)
	default:
		return fmt.Errorf("予期しないHTTPステータス: %d", status)
	}
}

// readHTTPResponse はステータス行とヘッダを読み取り、ステータスコードを返す。
// ヘッダ終端（空行）まで読み進めるため、その後 br から PCP アトムを続けて読める。
func readHTTPResponse(br *bufio.Reader) (int, error) {
	// ステータス行
	line, err := br.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("ステータス行の読み取り失敗: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	log.Printf("[http] %s", line)

	var proto string
	var code int
	var msg string
	if n, _ := fmt.Sscanf(line, "%s %d %s", &proto, &code, &msg); n < 2 {
		return 0, fmt.Errorf("ステータス行のパース失敗: %q", line)
	}

	// ヘッダ行（空行まで読み捨て）
	for {
		hdr, err := br.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("ヘッダ行の読み取り失敗: %w", err)
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if hdr == "" {
			break
		}
		log.Printf("[http] ヘッダ: %s", hdr)
	}

	return code, nil
}




