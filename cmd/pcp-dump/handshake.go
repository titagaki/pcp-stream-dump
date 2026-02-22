package main

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/titagaki/peercast-pcp/pcp"
)

// handle200 は HTTP 200 レスポンス後の PCP ハンドシェイクを完了し、
// ストリーム受信ループへ移行する。
func handle200(conn net.Conn, br *bufio.Reader, cfg config, outFile *os.File, doneCh <-chan struct{}) error {
	log.Printf("[200] PCP ハンドシェイク開始")

	if err := sendHelo(conn, cfg); err != nil {
		return err
	}

	// PCP_OK が来るまでアトムを読む（oleh などの中間アトムはログ出力してスキップ）
	for {
		atom, err := pcp.ReadAtom(br)
		if err != nil {
			return fmt.Errorf("PCP_OK 待機中の読み取り失敗: %w", err)
		}
		log.Printf("[recv] <<")
		logAtom(atom, "       ")

		switch atom.Tag {
		case pcp.PCPOK:
			log.Printf("[200] PCP_OK 受信、ストリーム受信ループを開始します")
			return runReceiveLoop(br, conn, outFile, doneCh)
		case pcp.PCPQuit:
			return fmt.Errorf("PCP_OK 待機中に PCP_QUIT を受信しました")
		}
	}
}

// handle503 は HTTP 503 レスポンス後に PCP ハンドシェイクを行い、
// PCP_HOST アトムから次の接続先を収集して再接続する。
func handle503(conn net.Conn, br *bufio.Reader, cfg config, outFile *os.File, doneCh <-chan struct{}, depth int) error {
	log.Printf("[503] リダイレクト先ノードを探索中...")

	if err := sendHelo(conn, cfg); err != nil {
		return err
	}

	// PCP_HOST または PCP_QUIT が来るまでアトムを読む
	var hosts []string
	for {
		atom, err := pcp.ReadAtom(br)
		if err != nil {
			return fmt.Errorf("503 ハンドシェイク中の読み取り失敗: %w", err)
		}
		log.Printf("[recv] <<")
		logAtom(atom, "       ")

		switch atom.Tag {
		case pcp.PCPHost:
			if host := parseHostAtom(atom); host != "" {
				log.Printf("[503] リダイレクト先候補: %s", host)
				hosts = append(hosts, host)
			}
		case pcp.PCPQuit:
			log.Printf("[503] PCP_QUIT 受信、候補ノード数: %d", len(hosts))
			if len(hosts) == 0 {
				return fmt.Errorf("リダイレクト先ノードが見つかりませんでした")
			}
			// 先頭の候補ノードへ再接続
			newCfg := cfg
			newCfg.addr = hosts[0]
			log.Printf("[503] %s へ再接続します", newCfg.addr)
			return connect(newCfg, outFile, doneCh, depth+1)
		}
	}
}

// sendHelo は helo コンテナアトムを送信する。
// agnt（エージェント名）、ver（バージョン）、sid（セッションID）、bcid（チャンネルID）を含む。
func sendHelo(w net.Conn, cfg config) error {
	// ランダムなセッションID を生成
	var sessID pcp.GnuID
	if _, err := rand.Read(sessID[:]); err != nil {
		return fmt.Errorf("セッションID生成失敗: %w", err)
	}

	heloAtom := pcp.NewParentAtom(
		pcp.PCPHelo,
		pcp.NewStringAtom(pcp.PCPHeloAgent, agentName),
		pcp.NewIntAtom(pcp.PCPHeloVersion, 1218),
		pcp.NewIDAtom(pcp.PCPHeloSessionID, sessID),
		pcp.NewIDAtom(pcp.PCPHeloBCID, cfg.channelID),
	)
	log.Printf("[send] >>")
	logAtom(heloAtom, "       ")
	if err := heloAtom.Write(w); err != nil {
		return fmt.Errorf("helo 送信失敗: %w", err)
	}
	return nil
}

// parseHostAtom は PCP_HOST コンテナアトムから "ip:port" 文字列を取り出す。
// IP は little-endian uint32、ポートは little-endian uint16 で格納されている。
func parseHostAtom(atom *pcp.Atom) string {
	var ipUint32 uint32
	var port uint16
	hasIP, hasPort := false, false

	for _, child := range atom.Children() {
		switch child.Tag {
		case pcp.PCPHostIP:
			v, err := child.GetInt()
			if err == nil {
				ipUint32 = v
				hasIP = true
			}
		case pcp.PCPHostPort:
			v, err := child.GetShort()
			if err == nil {
				port = v
				hasPort = true
			}
		}
	}

	if !hasIP || !hasPort || port == 0 {
		return ""
	}

	// PCP の IPv4 は little-endian uint32 で格納されているため
	// big-endian に変換して net.IP を構築する
	ipBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(ipBytes, ipUint32)
	ip := net.IP(ipBytes)

	return fmt.Sprintf("%s:%d", ip.String(), port)
}
