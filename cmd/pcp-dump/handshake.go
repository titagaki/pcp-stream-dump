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
	type hostEntry struct {
		addr     string
		canRelay bool
	}
	var hosts []hostEntry
	for {
		atom, err := pcp.ReadAtom(br)
		if err != nil {
			return fmt.Errorf("503 ハンドシェイク中の読み取り失敗: %w", err)
		}
		log.Printf("[recv] <<")
		logAtom(atom, "       ")

		switch atom.Tag {
		case pcp.PCPHost:
			addr, canRelay := parseHostAtom(atom)
			if addr != "" {
				log.Printf("[503] リダイレクト先候補: %s (relay=%v)", addr, canRelay)
				hosts = append(hosts, hostEntry{addr, canRelay})
			}
		case pcp.PCPQuit:
			log.Printf("[503] PCP_QUIT 受信、候補ノード数: %d", len(hosts))
			if len(hosts) == 0 {
				return fmt.Errorf("リダイレクト先ノードが見つかりませんでした")
			}
			// リレー可能なノードを優先し、先頭の候補へ再接続
			selected := hosts[0].addr
			for _, h := range hosts {
				if h.canRelay {
					selected = h.addr
					break
				}
			}
			newCfg := cfg
			newCfg.addr = selected
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

// parseHostAtom は PCP_HOST コンテナアトムから接続先アドレスとリレー可否を返す。
//
// PCP_HOST には IP/port ペアが最大2組含まれる（先頭がグローバルIP、2番目がローカルIP）。
// C++ 実装の readHostAtoms と同様に ipNum でインデックス管理し、先頭ペアを優先する。
// 0.0.0.0 やプライベートアドレスはスキップして最初の有効なグローバルIPを返す。
func parseHostAtom(atom *pcp.Atom) (addr string, canRelay bool) {
	type pair struct {
		ip   uint32
		port uint16
	}
	var pairs [2]pair
	ipNum := 0

	for _, child := range atom.Children() {
		switch child.Tag {
		case pcp.PCPHostIP:
			if ipNum < 2 {
				if v, err := child.GetInt(); err == nil {
					pairs[ipNum].ip = v
				}
			}
		case pcp.PCPHostPort:
			if ipNum < 2 {
				if v, err := child.GetShort(); err == nil {
					pairs[ipNum].port = v
					ipNum++
				}
			}
		case pcp.PCPHostFlags1:
			if v, err := child.GetByte(); err == nil {
				const flagRelay = 0x02
				canRelay = v&flagRelay != 0
			}
		}
	}

	// 先頭ペアから有効なグローバルIPを探す
	for i := 0; i < ipNum; i++ {
		p := pairs[i]
		if p.ip == 0 || p.port == 0 {
			continue
		}
		// little-endian uint32 → big-endian バイト列 → net.IP
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, p.ip)
		ip := net.IP(ipBytes)
		if isGlobalIP(ip) {
			return fmt.Sprintf("%s:%d", ip.String(), p.port), canRelay
		}
	}
	return "", canRelay
}

// isGlobalIP はプライベートアドレスおよびループバックアドレスを除いたグローバルIPかどうかを返す。
func isGlobalIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return false
	}
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
