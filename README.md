# pcp-stream-dump

PeerCast Protocol (PCP) のストリームを受信し、メディアペイロードをローカルファイルにダンプするCLIツールです。

## 要件

- Go 1.21 以上
- PeerCastノードへのネットワークアクセス

## ビルド

```sh
# リポジトリをクローン（またはディレクトリに移動）
cd /path/to/pcp-stream-dump

# 依存パッケージの取得
go mod download

# バイナリをビルド（カレントディレクトリに出力）
go build -o pcp-dump ./cmd/pcp-dump
```

## 使い方

```
pcp-dump <host:port> <channel-id> <output-file>
```

| 引数 | 説明 | 例 |
|---|---|---|
| `host:port` | 接続先 PeerCastノードのアドレスとポート | `localhost:7144` |
| `channel-id` | チャンネルID（32文字の16進数文字列） | `0123456789abcdef0123456789abcdef` |
| `output-file` | ダンプ先ファイルパス | `dump.flv` |

### 実行例

```sh
./pcp-dump localhost:7144 0123456789abcdef0123456789abcdef dump.flv
```

`go run` で直接実行することもできます。

```sh
go run ./cmd/pcp-dump localhost:7144 0123456789abcdef0123456789abcdef dump.flv
```

### 停止

`Ctrl+C`（SIGINT）または SIGTERM を送ると、グレースフルシャットダウンします。
ファイルとネットワーク接続を安全に閉じ、書き込んだ総バイト数をログ出力して終了します。

```
2026/02/22 12:34:56 signal received, shutting down...
2026/02/22 12:34:56 total bytes written: 10485760
```

## 動作の概要

PeerCast の PCP ストリーム受信プロトコルは、HTTP/1.0 アップグレード経由で行われます。

### 接続フロー

1. 指定アドレスへ TCP 接続を確立する
2. HTTP リクエストを送信する
   ```
   GET /channel/<channelIDHex> HTTP/1.0
   x-peercast-pcp:1
   ```
3. HTTP レスポンスのステータスコードで分岐する

#### 200 OK（ストリーム配信中）

4. `helo` コンテナ（`agnt` / `ver` / `sid` / `bcid`）を送信する
5. サーバーから `oleh`、続いて `ok` アトムを受信する
6. PCP アトムを継続的に読み込み、`pkt > data` アトムのペイロードを出力ファイルに追記する
7. 進捗（書き込み済みバイト数）を5秒ごとにログ出力する

#### 503 Service Unavailable（リレー先へリダイレクト）

4. `helo` コンテナを送信する
5. サーバーから `host` アトムで接続候補ノード一覧を受信する
6. `quit` アトム受信後、先頭の候補ノードへ再接続する（最大8回）

#### 404 Not Found

チャンネルが存在しないためエラー終了する。

## 注意事項

- 出力ファイルは**追記モード**で開きます。毎回新規にダンプしたい場合は実行前にファイルを削除してください。
  ```sh
  rm -f dump.flv && ./pcp-dump localhost:7144 <channel-id> dump.flv
  ```
- チャンネルIDは必ず32文字の16進数文字列で指定してください。PeerCastの管理画面などで確認できます。

## License

This project is licensed under the GNU General Public License v3.0.
Portions of this software are Copyright (C) 2026 ITAGAKI Takayuki