package main

import (
	"log"
	"os"

	"github.com/titagaki/peercast-pcp/pcp"
)

// processPacket はアトムツリーを走査し、ストリームデータのペイロードを出力ファイルに書き込む。
// 書き込んだバイト数を返す。
func processPacket(pkt *pcp.Atom, out *os.File) (int, error) {
	if pkt == nil {
		return 0, nil
	}

	if !pkt.IsParent() {
		if pkt.Tag == pcp.PCPChanPktData {
			payload := pkt.Data()
			if len(payload) == 0 {
				log.Printf("[data] tag=%s: empty payload, skipping", pkt.Tag)
				return 0, nil
			}
			log.Printf("[data] tag=%s: %d bytes  preview=%s", pkt.Tag, len(payload), hexPreview(payload))
			n, err := out.Write(payload)
			if err != nil {
				return 0, err
			}
			return n, nil
		}
		return 0, nil
	}

	total := 0
	for _, child := range pkt.Children() {
		n, err := processPacket(child, out)
		if err != nil {
			return total, err
		}
		total += n
	}

	return total, nil
}
