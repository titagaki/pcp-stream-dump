package main

import (
	"encoding/hex"
	"log"
	"strings"

	"github.com/titagaki/peercast-pcp/pcp"
)

// logAtom はアトムを人間が読みやすいツリー形式でログに出力する。
func logAtom(a *pcp.Atom, indent string) {
	if a == nil {
		log.Printf("%s<nil>", indent)
		return
	}
	if a.IsParent() {
		children := a.Children()
		log.Printf("%s[%s] container  children=%d", indent, a.Tag, len(children))
		for _, child := range children {
			logAtom(child, indent+"  ")
		}
	} else {
		data := a.Data()
		log.Printf("%s[%s] data  len=%d  %s", indent, a.Tag, len(data), hexPreview(data))
	}
}

// hexPreview はデータの先頭最大16バイトをスペース区切りの16進数文字列で返す。16バイトを超える場合は末尾に "..." を付加する。
func hexPreview(b []byte) string {
	const max = 16
	if len(b) == 0 {
		return ""
	}
	truncated := len(b) > max
	if truncated {
		b = b[:max]
	}
	s := hex.EncodeToString(b)
	// 2文字ごとにスペースを挿入して読みやすくする
	var sb strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(s[i : i+2])
	}
	if truncated {
		sb.WriteString(" ...")
	}
	return sb.String()
}
