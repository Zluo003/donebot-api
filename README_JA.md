# DoneBot API Gateway

[中文](README_CN.md) · [English](README.md) · 日本語

DoneBot 専用のモデルゲートウェイです。開発・配布元は **[Zluo003/donebot-api](https://github.com/Zluo003/donebot-api)** です。sub2api を基盤とし、Anthropic / OpenAI / Gemini のネイティブなリクエスト・レスポンス形式と、既存の認証・残高・従量課金を利用します。

## 機能

- DoneBot composite グループとアカウントで有効なモデルの一覧。LLM はチャネル価格 × モデル別倍率、画像・音声・動画はモデル別販売単価を使い、既存の課金処理を利用します。
- ログインしたユーザーと端末に紐づくインストール認証情報。DoneBot モードでは通常の API Key 管理を非表示にします。
- LLM、同期・非同期画像生成、ローカル / S3 互換ストレージ（R2、OSS、MinIO）。
- NewToken 経由の Seedance 2.0 / Fast / Mini / 2.5。最初のフレーム、最初と最後のフレーム、参照素材、2.5 の動画編集・延長に対応します。動画アカウント専用パネルで解像度を選択できます。
- APIMart Seedream 5.0 Flash、Suno の歌曲・器楽生成、TTS。専用アカウントパネルと音声モデル管理を提供します。
- LLM は token、画像は実際の枚数・サイズ、Seedance は出力秒数、Suno は生成回数、TTS は Unicode 文字数で課金します。

## Linux インストール

Linux `amd64` / `arm64`、systemd、Bash 4+、PostgreSQL、Redis が必要です。インストーラーは**このリポジトリ**の Release を取得し、`checksums.txt` を検証します。互換性のためバイナリ名とサービス名は `sub2api` のままです。

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/install.sh | sudo bash
sudo systemctl enable --now sub2api
sudo journalctl -u sub2api -f
```

`http://サーバーアドレス:8080` で初期設定を完了してください。PostgreSQL と Redis は別途準備します。

[Releases](https://github.com/Zluo003/donebot-api/releases) には `sub2api_<version>_linux_amd64.tar.gz` と `sub2api_<version>_linux_arm64.tar.gz` を配布します。Web UI はバイナリに含まれます。詳細は[デプロイガイド](deploy/README.md)を参照してください。

## Docker デプロイ

```bash
mkdir -p donebot-api-deploy && cd donebot-api-deploy
curl -fsSL https://raw.githubusercontent.com/Zluo003/donebot-api/main/deploy/docker-deploy.sh | bash
# 生成された .env と設定を確認してから起動します。
docker compose up -d
docker compose logs -f sub2api
```

イメージ名は `ghcr.io/zluo003/donebot-api` です。公開タグは [Packages](https://github.com/Zluo003?tab=packages&repo_name=donebot-api)、Linux バイナリは Releases で確認してください。サービス名とデータパスは `sub2api` を維持します。

## DoneBot モード

このリポジトリのデプロイテンプレートは DoneBot モードを標準で有効にします。既存の環境では `config.yaml` に次の設定を追加するか、対応する環境変数 `DONEBOT_ENABLED`、`DONEBOT_GROUP_NAME`、`DONEBOT_AUTO_CREATE_GROUP`、`DONEBOT_FEATURES_VIDEO`、`DONEBOT_FEATURES_AUDIO` を設定してください。

```yaml
donebot:
  enabled: true
  group_name: DoneBot
  auto_create_group: true
  features:
    video: false
    audio: false
```

Docker では生成された `.env`、バイナリインストールでは `/etc/sub2api/environment` を編集します。ローカル素材と非同期画像を使う前に `ASSET_STORAGE_PUBLIC_BASE_URL=https://ゲートウェイの公開ドメイン` を設定し、`/assets/*` をゲートウェイへプロキシしてください。変更後は再起動します。Docker では設定全体を `/app/data/config.yaml` にマウントすることもできます。詳細は[設定例](deploy/config.example.yaml)と[導入記録](docs/donebot/验收记录.md)を参照してください。

DoneBot → ストレージでアップロード素材と生成結果の共有ポリシーを管理します。容量、保存期間、ローカル/S3 の設定は[ストレージガイド](docs/donebot/存储管理说明.md)を参照してください。

上流アカウントを有効にして DoneBot グループに紐づけ、許可モデルを設定します。DoneBot で同期し、LLM のモデル別倍率またはメディア販売単価とモデルの有効状態を一度に保存します。音声・動画の実行機能が無効でも設定画面は使用できます。[価格ガイド](docs/donebot/模型定价说明.md)を参照してください。ログイン済みユーザーは `POST /api/v1/donebot/installations/claim` から端末の認証情報を取得します。認証情報は初回のみ返され、そのユーザーと端末に紐づきます。

Seedance を使う場合は `DONEBOT_FEATURES_VIDEO=true` を設定し、動画アカウントの上流 URL を `https://api.newtoken.club` にします。公式解像度、`asset://` / 公開 URL 素材、編集・延長と秒単価については [Seedance ガイド](docs/donebot/Seedance-接入说明.md)を参照してください。APIMart の上流 URL は `https://api.apimart.ai` です。Suno / TTS は `DONEBOT_FEATURES_AUDIO=true` で有効化します。[APIMart ガイド](docs/donebot/APIMart-接入说明.md)を参照してください。管理者はサイドバーのバージョン表示から正式バイナリをオンライン更新できます。

## ソースからビルド

[`backend/go.mod`](backend/go.mod) の Go バージョン、Node.js 24、pnpm 9 を使用します。

```bash
git clone https://github.com/Zluo003/donebot-api.git
cd donebot-api
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build
cd backend
GOTOOLCHAIN=auto CGO_ENABLED=0 go build -tags=embed -trimpath -o bin/sub2api ./cmd/server
```

## ドキュメント・問い合わせ

- [デプロイガイド](deploy/README.md)
- [ストレージ管理](docs/donebot/存储管理说明.md)
- [DoneBot ゲートウェイドキュメント](docs/donebot/README.md)
- [モデル価格](docs/donebot/模型定价说明.md)
- [APIMart](docs/donebot/APIMart-接入说明.md)
- [Seedance](docs/donebot/Seedance-接入说明.md)
- [Issues](https://github.com/Zluo003/donebot-api/issues) · [Releases](https://github.com/Zluo003/donebot-api/releases)

本プロジェクトは **[Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)** の派生版です。上流の作者と貢献者に感謝します。原著者の表示を保持し、[GNU LGPL v3](LICENSE) に従います。
