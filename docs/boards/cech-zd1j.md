# Sony CECH-ZD1 J（バックエンド`cech-zd1j`）

| | |
| --- | --- |
| バックエンド | `cech-zd1j`（別名`mb86h47`）|
| 対応OS | Linux / Windows / macOS（直接USB）|
| 伝送方式 | ISDB-T |
| ファームウェア | 不要 |

富士通のMB86H47Aと、DiBcomのDiB8096（DiB8000復調器とDiB0090チューナーを1パッケージにしたもの）が載っています。

## 対応製品

| 製品 | VID:PID | チューナー本数 | 伝送方式 |
| --- | --- | --- | --- |
| Sony CECH-ZD1 J | `054c:04b2` | 1 | ISDB-T |

## LED

前面のLEDは、DiB8096のGPIO 1につながっています。
GPIOを出力にしてHighにすると赤、Lowにするか入力のままにすると緑です。
`coaxd`は、チューナーを使っている間はLEDを赤にし、使い終わると緑に戻します。

## 定義ファイル

共通の書式は[channel-files.md](../reference/channel-files.md)にあります。
`FREQUENCY`だけが必要で、6MHz幅のISDB-T（13セグメント）として選局します。
伝送パラメーター（モード、ガードインターバル、各階層の変調方式）はDiB8000が自動で判別します。

