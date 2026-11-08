# srtrx — SRT受信アプリ

MediaMTX が受けた SRT 映像を、ffmpeg でデコードして展示ディスプレイへ全画面で映す Go 製の GUI アプリ。
伝送の断も、ffmpeg やプレイヤーの落下も日常として扱い、**画面を暗転させない**。
ロス・ドロップ・再送・RTT などの統計を、映像の上端に重ねて見せる。

仕様は「SRT受信アプリ 仕様書」に従う。実装中の判断は [`docs/DECISIONS.md`](docs/DECISIONS.md)、
実機で確かめる手順と結果は [`docs/VERIFY.md`](docs/VERIFY.md) にある。

## 構成

```
MediaMTX(同梱・制御の子) ──SRT──▶ ffmpeg(制御の子) ──生フレーム(rgb24)──▶ プレイヤー(常駐)
        ▲                                  ▲                                      │
        └────────── 制御(srtrx): 設定GUI、監督、/metrics の取得と累計 ◀──ハートビート──┘
```

| 実行ファイル | 役割 |
| --- | --- |
| `srtrx`(制御) | 設定GUI、MediaMTX と ffmpeg の監督、`/metrics` の取得と累計、プレイヤーへの送信 |
| `srtrx-player`(プレイヤー) | フレームの受信と全画面表示、オーバーレイ、状態バッジ、ハートビート |

プレイヤーは独立して常駐するので、MediaMTX や ffmpeg が入れ替わっても、ウィンドウと最後のフレームが残る。

## 必要なもの(Ubuntu 24.04 LTS, x86_64)

```bash
sudo apt-get install -y libgtk-4-dev libgirepository1.0-dev pkg-config ffmpeg
```

- GTK 4.10 以上、ffmpeg 6.0 以上(**libsrt が有効**なビルド。起動時に自己診断する)
- Go 1.22 以上(依存の都合で、より新しい版が要る場合は `GOTOOLCHAIN=auto` が取得する)
- gotk4 は **`pkg v0.3.1`** に固定している(v0.4.1 は新しい glib を前提とし、24.04 では cgo のビルドが通らない)

## ビルドと配備

```bash
make build      # MediaMTX の公式バイナリを取得(SHA-256 検証)し、bin/srtrx と bin/srtrx-player を作る
make test       # go test ./...
make install    # ~/.local/bin とユーザーの systemd ユニットへ配置

systemctl --user daemon-reload
systemctl --user enable --now srtrx-player.service srtrx.service
```

初回の gotk4 のビルドは長い(数分〜10分)。GitHub Actions(`.github/workflows/ci.yaml`)でもビルドと検証が走る。

## 使い方

- 制御のウィンドウに「状態」「設定」「ログ」の3画面がある。ヘッダーに「本番ロック」と「情報」(MediaMTX のライセンス)。
- 設定の「即時」の項目は、変えた瞬間に反映される。「再起動」の項目は **「適用して再起動」** でまとめて反映する(確認ダイアログが出る)。
- 範囲外の値を入れた欄は赤くなり、「適用」を押せなくなる。
- 本番ロック中は設定を変更できない。解除はロックボタンと確認ダイアログで行う。
- 累計のリセット: 制御ウィンドウの「カウンターをリセット」ボタン、`Ctrl+R`、または制御プロセスへ `SIGUSR1`。
- プレイヤーのホットキー(既定 `o`)で、オーバーレイ全体の表示を切り替える。
- `srtrx -headless` は GUI 無しで動かす(監督と統計のみ。1秒ごとに状態を1行でログへ出す)。

設定ファイルは `~/.config/srtrx/config.toml`(TOML)。プロファイルは同じ場所の `profiles/<名前>.toml`。
壊れていれば `config.toml.bak1` → `bak2` → `bak3` の順に使い、全部だめなら既定値で起動する。

## 送信側(OBS)の設定

- **キーフレーム間隔は 1〜2秒にする。** 受信側は、送信の途中から繋ぐと、次のキーフレームまで映像を出せない。
  間隔が長い(OBS の「自動」など)と、起動や再接続のたびに映像が出るまで待たされる。
- SRT の `latency` は、想定する断の長さより長くする(受信機の `input.latency_ms` は、MediaMTX→ffmpeg の区間にしか効かない)。
- OBS の「自動再接続」を有効にする。

## 開発用

```bash
go run ./tools/feed -n 90        # ffmpeg の代わりに、テストフレームをプレイヤーへ送る
SRTRX_FFMPEG=/path/to/ffmpeg srtrx   # PATH 外の ffmpeg を使う
```

---

# リポジトリ運用(テンプレート由来)

NCチームで使用する開発環境のテンプレートです．

## セットアップ

### 1. ブランチ保護ルールの設定（Rulesets）

`main` ブランチへの直接プッシュを禁止し，プルリクエスト経由のマージを強制します．
GitHub の新しい **Rulesets** を使用して設定します．

#### 設定手順

1. GitHub リポジトリの **Settings** > **Rules** > **Rulesets** を開く
2. **New ruleset** > **New branch ruleset** をクリック
3. 以下を設定して **Save changes** をクリックする

#### Ruleset 設定内容

| 項目 | 値 |
| --- | --- |
| Ruleset name | `pullreq`（任意） |
| Enforcement status | Active |
| Target branches | Default branch（`main`） |

#### Bypass list

| ロール | 許可内容 |
| --- | --- |
| Organization admin | Allow for pull requests only |
| Repository admin | Always allow |

#### 有効にするルール

| ルール | 説明 |
| --- | --- |
| Restrict deletions | ブランチの誤削除を防ぐ |
| Require a pull request before merging | マージ前に PR を必須にする |
| Require status checks to pass | CI テストが通過しないとマージ不可 |
| Block force pushes | 履歴の強制書き換えを禁止する |
| Automatically request Copilot code review | PR 作成時に Copilot によるコードレビューを自動リクエストする |

---

### 2. 開発環境のセットアップ（Dev Container）

Dev Container を使用することで，チーム全員が同一の開発環境を再現できます．

#### 前提条件

- [Docker](https://www.docker.com/products/docker-desktop/) がインストール済みであること
- [Visual Studio Code](https://code.visualstudio.com/) がインストール済みであること
- VS Code 拡張機能 [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) がインストール済みであること

#### 手順

1. VS Code でリポジトリのルートディレクトリを開く
2. コマンドパレットを開く（`Cmd+Shift+P` / `Ctrl+Shift+P`）
3. `Dev Containers: Reopen in Container` を選択する
4. コンテナのビルドが完了するまで待つ

コンテナ起動後は，`.devcontainer/devcontainer.json` に定義された環境が自動的に適用されます．

---

### 3. チケット駆動開発のブランチ運用

GitHub Issues をチケットとして使用し，1 チケット 1 ブランチで作業を管理します．

#### ブランチ運用フロー

```text
main
 └─ dev
     └─ feat/*** ─── 作業 ─── PR ──→ dev ─── PR ──→ main
```

##### feat/\*\*\* → dev（日常の開発）

1. GitHub Issues で `[FEAT]` チケットを作成する
2. `dev` ブランチから `feat/***` ブランチを切る（ブランチ名はチケットに記載）
3. `feat/***` ブランチで作業を行う
4. 作業完了後，`feat/***` から `dev` へ PR を作成してマージする

##### dev → main（リリース）

`dev` から `main` へマージするには，以下の条件をすべて満たす必要があります．

| 条件 | 状態 |
| --- | --- |
| CI テストが全て通過していること | 必須 |
| CD によるステージング環境へのデプロイが成功していること | 予定 |
| 開発者がログ・メトリクス・トレースの取得を確認していること | 予定 |

---

### 4. コミットメッセージテンプレートの設定

`.gitmessage` をコミットメッセージのテンプレートとして使用します．
リポジトリをクローン後，以下のコマンドを **1回だけ** 実行してください．

```bash
git config commit.template .gitmessage
```

設定後は `git commit` を実行すると，以下のテンプレートがエディタに表示されます．

```text
# feat | fix | docs | refactor | test | chore
<type>: <subject>

Refs: #
```

| type | 用途 |
| --- | --- |
| `feat` | 新機能の追加 |
| `fix` | バグ修正 |
| `docs` | ドキュメントのみの変更 |
| `refactor` | 機能変更を伴わないコード改善 |
| `test` | テストの追加・修正 |
| `chore` | ビルド・設定などの雑務 |

---

#### RACI

各チケットには以下の役割を記載します．**R はチケット作成者自身**が担います．

| 役割 | 説明 |
| --- | --- |
| R: 実行責任者 (Responsible) | 実際に作業を行う人．チケット作成者が担当する |
| A: 説明責任者 (Accountable) | 成果物に対して最終責任を持つ人 |
| C: 協業先 (Consulted) | 作業に際して相談・協力を求める人 |
| I: 報告先 (Informed) | 進捗・完了を報告する人 |
