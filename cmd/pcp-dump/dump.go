package main

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/titagaki/peercast-pcp/pcp"
)

// runReceiveLoop は br からアトムを読み続け、ストリームデータを outFile に書き込む。
// doneCh が閉じられるか、接続が切断されると終了する。
// conn は doneCh 受信時のシャットダウン（Read のブロック解除）に使用する。
func runReceiveLoop(br *bufio.Reader, conn net.Conn, outFile *os.File, doneCh <-chan struct{}) error {
	var totalBytes int64
	lastReport := time.Now()

	for {
		select {
		case <-doneCh:
			log.Printf("[recv] シャットダウン要求を受信 (合計書き込み: %d bytes)", totalBytes)
			return nil
		default:
		}

		atom, err := pcp.ReadAtom(br)
		if err != nil {
			select {
			case <-doneCh:
				log.Printf("[recv] シャットダウンによる接続終了 (合計書き込み: %d bytes)", totalBytes)
				return nil
			default:
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					log.Printf("[recv] EOF: 接続が閉じられました (合計書き込み: %d bytes)", totalBytes)
					return nil
				}
				return err
			}
		}

		log.Printf("[recv] <<")
		logAtom(atom, "       ")

		n, err := processPacket(atom, outFile)
		if err != nil {
			return err
		}
		totalBytes += int64(n)

		if n > 0 {
			log.Printf("[write] %d bytes 書き込み (合計: %d bytes)", n, totalBytes)
		}
		if time.Since(lastReport) >= 5*time.Second {
			log.Printf("[progress] 書き込み済みバイト数: %d", totalBytes)
			lastReport = time.Now()
		}
	}
}

// processPacket はアトムツリーを再帰的に走査し、
// data アトムのペイロードを出力ファイルに書き込む。書き込んだバイト数を返す。
func processPacket(atom *pcp.Atom, out *os.File) (int, error) {
	if atom == nil {
		return 0, nil
	}

	// data アトム（ストリームペイロード）を書き込む
	if !atom.IsParent() {
		if atom.Tag == pcp.PCPChanPktData {
			payload := atom.Data()
			if len(payload) == 0 {
				log.Printf("[data] tag=%s: ペイロードが空のためスキップ", atom.Tag)
				return 0, nil
			}
			log.Printf("[data] tag=%s: %d bytes  preview=%s", atom.Tag, len(payload), hexPreview(payload))
			n, err := out.Write(payload)
			if err != nil {
				return 0, err
			}
			return n, nil
		}
		return 0, nil
	}

	// コンテナアトムは子を再帰的に処理する
	total := 0
	for _, child := range atom.Children() {
		n, err := processPacket(child, out)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
