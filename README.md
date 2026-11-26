# tcpraw

## 🇵🇱Polski

Przesyłanie plików przez TCP z 6-cyfrowymi kodami. Kod generuje klient i szyfruje plik; serwer przechowuje dane w postaci zaszyfrowanej. Bez rejestracji.

### Funkcje

- **Kod generuje klient** – 6-cyfrowy kod powstaje na Twoim komputerze; serwer nie zna klucza.
- **Szyfrowanie** – Dane są szyfrowane (AES-256-GCM) kluczem z kodu przed wysłaniem. Przechowywane i przesyłane w formie zaszyfrowanej.
- **Secure send** – Tryb z własnym kluczem 256-bit: serwer przypisuje kod, klient trzyma klucz; pobieranie tylko z klientem i podaniem klucza. Pliki >500 MB są strumieniowane (max 500 MB w RAM).
- **Checksum** – Weryfikacja SHA256 przy wysyłce i pobieraniu.
- **Pobieranie w przeglądarce** – Opcjonalna strona HTTP: otwórz w przeglądarce, wpisz kod, pobierz bez instalacji klienta (tylko uploady zwykłe „send”; secure wymaga klienta i klucza).
- **Limit prób** – Limit sprawdzeń kodu na IP (domyślnie 50 na 10 min); przekroczenie = ban 15 minut.
- **Long-term** – Opcjonalnie plik można przechować dłużej niż 30 minut (np. 7 dni): klient `--longterm=7d` / `-t 7d`, max 150 MB; serwer musi być uruchomiony z `--longterm` / `-t`, inaczej odrzuca takie uploady (domyślnie wyłączone).
- **Total network storage** – Uruchomienie `tcpraw` (bez argumentów lub z nieznaną komendą) pokazuje łączne wolne miejsce na wszystkich serwerach z listy.
- **Konfiguracja** – Czas przechowywania, odstępy czyszczenia, max rozmiar uploadu i limity ustawiasz w `main.go`; lista serwerów w kodzie (pierwsza cyfra kodu = id serwera). **Każdy serwer** może przy starcie ustawić **własny** limit rozmiaru pliku (`--maxsize=MB` / `-m MB`).

### Wymagania

- Go 1.21+

### Instalacja (Linux)

Przez APT:

```bash
echo "deb [trusted=yes] https://hdmain.github.io/rawuploader ./" \
  | sudo tee /etc/apt/sources.list.d/tcpraw.list
sudo apt update
sudo apt install tcpraw
```

Alternatywnie przez skrypt:

```bash
curl -sSL https://raw.githubusercontent.com/hdmain/rawuploader/main/install.sh | bash
```

### Instalacja (Windows)

W PowerShell (jako administrator):

```powershell
irm https://raw.githubusercontent.com/hdmain/rawuploader/main/install-win.ps1 | iex
```

### Konfiguracja (main.go)

Domyślne wartości zmieniasz w zmiennych na początku pliku `main.go`:


| Zmienna             | Domyślnie            | Opis                                      |
| ------------------- | -------------------- | ----------------------------------------- |
| (lista serwerów)    | w kodzie `client.go` | Adresy serwerów; pierwsza cyfra kodu = id |
| `StorageDuration`   | `30 * time.Minute`   | Jak długo przechowywane są dane           |
| `CleanupInterval`   | `5 * time.Minute`    | Co ile usuwane są wygasłe bloby           |
| `MaxBlobSize`       | 15 GB                | Domyślny max rozmiar uploadu (bajty); każdy serwer może nadpisać przez `--maxsize=MB` / `-m MB`. |
| `RateLimitAttempts` | 50                   | Maks. sprawdzeń kodu na IP w oknie        |
| `RateLimitWindow`   | `10 * time.Minute`   | Okno czasowe limitu                       |
| `BanDuration`       | `15 * time.Minute`   | Czas bana po przekroczeniu limitu         |


### Komendy


| Komenda              | Opis                                                                                            |
| -------------------- | ----------------------------------------------------------------------------------------------- |
| `tcpraw server`      | Uruchamia serwer (przechowuje zaszyfrowane bloby).                                              |
| `tcpraw servers`     | Test każdego serwera osobno: ping, wolne miejsce, 2 s download i 2 s upload losowych danych (symulacja losowego pliku); tabela z wynikami. |
| `tcpraw send`        | Wysyła plik; generuje 6-cyfrowy kod, szyfruje, wypisuje kod. Opcja `--server` / `-s`.           |
| `tcpraw secure send` | Wysyła plik z własnym kluczem 256-bit; serwer przypisuje kod. Opcja `--server` / `-s`.          |
| `tcpraw get`         | Pobiera plik po 6-cyfrowym kodzie (odszyfrowuje; dla secure – podaj klucz).                     |
| `tcpraw <plik> -l`   | Skrót: wysyłka w sieci LAN bez serwera (`--local` też działa).                                  |
| `tcpraw get -l`      | Odbiór pliku w sieci LAN bez serwera (`--local` też działa).                                    |

Każda opcja ma formę krótką (`-x`) i długą (`--nazwa`). Każda komenda obsługuje `-h` i `--help` (kod wyjścia 0). Uruchomienie bez argumentów lub z nieznaną komendą pokazuje pomoc (kod wyjścia 1).

**Kody wyjścia:** `0` – sukces, `1` – błąd użycia (złe argumenty), `2` – błąd wykonania.


### Argumenty i opcje


| Komenda              | Argument / opcja  | Opis                                                           |
| -------------------- | ----------------- | -------------------------------------------------------------- |
| `tcpraw server`      | `-i` / `--id`        | Id serwera (pierwsza cyfra generowanych kodów); domyślnie 0.   |
| `tcpraw server`      | `-p` / `--port`      | Port TCP (domyślnie 9999).                                     |
| `tcpraw server`      | `-d` / `--dir`       | Katalog na bloby (domyślnie `./data`).                         |
| `tcpraw server`      | `-w` / `--web`       | Port HTTP strony pobierania w przeglądarce; pomiń = wyłączone. |
| `tcpraw server`      | `-m` / `--maxsize`   | Osobny limit max rozmiaru uploadu dla tego serwera w MB (0 = domyślna z kodu). |
| `tcpraw server`      | `-t` / `--longterm`  | Włącza obsługę long-term (przechowywanie np. 7 dni; domyślnie wyłączone).     |
| `tcpraw send`        | `-s` / `--server`    | Użyj serwera o podanym id z listy (domyślnie: auto).           |
| `tcpraw send`        | `-t` / `--longterm`  | Przechowuj plik np. 7 dni (max 150 MB; serwer musi mieć `--longterm`).        |
| `tcpraw send`        | `-z` / `--zip`    | Spakuj plik lub katalog do tar.gz przed wysłaniem.             |
| `tcpraw send`        | `-l` / `--local`  | Tryb LAN (bez serwera).                                        |
| `tcpraw send`        | `<plik>`          | Ścieżka do pliku do wysłania.                                  |
| `tcpraw send`        | `[host:port]`     | Opcjonalnie: adres serwera (nadpisuje listę).                  |
| `tcpraw secure send` | `-s` / `--server` | Użyj serwera o podanym id z listy (domyślnie: auto).           |
| `tcpraw secure send` | `-t` / `--longterm` | Przechowuj plik np. 7 dni (max 150 MB; serwer musi mieć `--longterm`).      |
| `tcpraw secure send` | `-z` / `--zip`    | Spakuj plik lub katalog do tar.gz przed wysłaniem.             |
| `tcpraw secure send` | `<plik>`          | Ścieżka do pliku do wysłania.                                  |
| `tcpraw secure send` | `[host:port]`     | Opcjonalnie: adres serwera (nadpisuje listę).                  |
| `tcpraw get`         | `<6-cyfrowy-kod>` | Kod zwrócony przy wysyłce.                                     |
| `tcpraw get`         | `-o` / `--output` | Nazwa zapisanego pliku (domyślnie: z serwera).                 |
| `tcpraw get`         | `-u` / `--unzip`  | Po pobraniu rozpakuj tar.gz i usuń archiwum.                   |
| `tcpraw get`         | `-l` / `--local`  | Tryb odbioru LAN (bez serwera).                                |


### Użycie

**Serwer:**

```bash
tcpraw server -p 9999 -d ./data
```

Z włączoną stroną do pobierania w przeglądarce (bez klienta):

```bash
tcpraw server --port 9999 --dir ./data --web 8080
```

Następnie otwórz `http://SERVER:8080` i wpisz 6-cyfrowy kod, żeby pobrać plik.

- **-i / --id** – Id serwera 0–9 (pierwsza cyfra generowanych kodów); domyślnie 0.
- **-p / --port** – Port TCP protokołu tcpraw (domyślnie 9999).
- **-d / --dir** – Katalog na zaszyfrowane bloby (domyślnie `./data`).
- **-w / --web** – Port HTTP strony pobierania; pomiń, żeby wyłączyć.
- **-m / --maxsize** – Osobny limit max rozmiaru uploadu dla tego serwera w MB (0 = domyślna z kodu). Każda instancja serwera może mieć inny limit.
- **-t / --longterm** – Włącza obsługę long-term: klient może wysłać plik z `--longterm=7d` (lub np. `-t 24h`), wtedy plik jest przechowywany przez podany czas zamiast 30 minut. Maks. rozmiar long-term to 150 MB. Domyślnie wyłączone.

Dane są zapisywane na dysku. Przy starcie usuwane są stare i wygasłe bloby.

**Send (wysyłanie):**

```bash
tcpraw send [-s 0..9|--server=0..9] [-t 7d|--longterm=7d] [-z|--zip] [-l|--local] <plik> [host:port]
```

Wysyła plik, szyfruje go nowym 6-cyfrowym kodem i wypisuje kod. Opcja `--server=0..9` / `-s 0..9` wybiera serwer z listy (domyślnie: auto). Opcja `--longterm=7d` / `-t 7d` przechowuje plik przez podany czas zamiast 30 minut – max 150 MB, serwer musi być uruchomiony z `--longterm` / `-t`. Opcjonalnie `host:port` nadpisuje adres.

**Secure send (wysyłanie z własnym kluczem):**

```bash
tcpraw secure send [-s 0..9|--server=0..9] [-t 7d|--longterm=7d] [-z|--zip] <plik> [host:port]
```

Szyfruje plik 256-bitowym kluczem (generowanym przez klienta). Serwer przypisuje 6-cyfrowy kod i przechowuje dane zaszyfrowane; **klucza nie zna**. Po uploadzie dostajesz kod i klucz (64 znaki hex) – bez klucza pliku nie da się odszyfrować. Dla plików >500 MB dane są strumieniowane (w RAM nie więcej niż ~500 MB). Opcja `--longterm=7d` / `-t 7d` – max 150 MB, serwer musi mieć `--longterm` / `-t`.

**Get (pobieranie):**

```bash
tcpraw get <6-cyfrowy-kod> [-o plik|--output plik] [-u|--unzip]
tcpraw get -l
```

Pobiera plik po podanym kodzie. Dla uploadu zwykłego „send” odszyfrowanie jest po kodzie. Dla „secure send” program poprosi o klucz (64 znaki hex). Opcja `-o` / `--output` ustawia nazwę zapisanego pliku. Opcja `-u` / `--unzip` rozpakowuje tar.gz po pobraniu.

### Protokół w skrócie

- **Upload (send):** Klient wysyła typ `U`, 6-bajtowy kod, zaszyfrowane dane (nazwa, checksum, nonce, sealed). Serwer zapisuje pod kodem i zwraca status. Duże pliki mogą być wysyłane chunkami (format chunked).
- **Upload (secure send):** Klient wysyła typ `S`; format 0 = jeden blob (plik ≤500 MB w RAM), format 1 = chunked (plik >500 MB, strumieniowo). Serwer zapisuje zaszyfrowane dane, generuje kod, zwraca kod; klucza nie zna.
- **Download:** Klient wysyła typ `D` i 6-bajtowy kod. Serwer zwraca status i bajt formatu (0 = pojedynczy blob, 1 = chunked zwykły, 2 = secure pojedynczy, 3 = secure chunked). Klient odszyfrowuje (kodem lub kluczem) i sprawdza checksum.
- **Web:** GET `/` pokazuje formularz; GET `/get?code=XXXXXX` zwraca plik jako załącznik tylko dla uploadów zwykłych (serwer odszyfrowuje kodem). Pliki z „secure send” wymagają klienta i klucza.

### Limit prób (rate limiting)

- Każda próba pobrania po kodzie (TCP lub strona) jest liczona per IP.
- Domyślnie: 50 prób na 10 minut na IP; potem ten IP jest zbanowany na 15 minut.
- Dotyczy zarówno protokołu TCP, jak i strony do pobierania.

### Licencja

Możesz używać i modyfikować dowolnie.

---

## 🇺🇸English

TCP file send/receive with 6-digit codes. The client generates the code and encrypts the file; the server stores data encrypted. No account needed.

### Features

- **Client generates code** – 6-digit code is created on your machine; the server never sees the key.
- **Encryption** – Data is encrypted (AES-256-GCM) with a key derived from the code before upload. Stored and transmitted encrypted.
- **Secure send** – Mode with your own 256-bit key: server assigns the code, client keeps the key; download only with the client and the key. Files >500 MB are streamed (max 500 MB in RAM).
- **Checksum** – SHA256 verification on upload and download.
- **Web download** – Optional HTTP page: open in a browser, enter the code, download without installing the client (only for regular “send” uploads; secure uploads require the client and the key).
- **Rate limiting** – Per-IP limit on code checks (default 50 per 10 min); excess leads to a 15-minute ban.
- **Total network storage** – Running `tcpraw` with no arguments or an unknown command shows total free space across all servers from the list.
- **Configurable** – Storage duration, cleanup interval, max upload size, and rate limits are set in `main.go`; server list is in code (first digit of code = server id). **Each server** can set its **own** max blob size at startup (`--maxsize=MB` / `-m MB`).

### Requirements

- Go 1.21+

### Installation (Linux)

Via APT:

```bash
echo "deb [trusted=yes] https://hdmain.github.io/rawuploader ./" \
  | sudo tee /etc/apt/sources.list.d/tcpraw.list
sudo apt update
sudo apt install tcpraw
```

Alternatively via script:

```bash
curl -sSL https://raw.githubusercontent.com/hdmain/rawuploader/main/install.sh | bash
```

### Installation (Windows)

In PowerShell (run as Administrator):

```powershell
irm https://raw.githubusercontent.com/hdmain/rawuploader/main/install-win.ps1 | iex
```

### Configuration (main.go)

Edit the variables at the top of `main.go` to change defaults:


| Variable            | Default            | Description                                |
| ------------------- | ------------------ | ------------------------------------------ |
| (server list)       | in `client.go`     | Server addresses; first digit of code = id |
| `StorageDuration`   | `30 * time.Minute` | How long blobs are kept                    |
| `CleanupInterval`   | `5 * time.Minute`  | How often expired blobs are removed        |
| `MaxBlobSize`       | 15 GB              | Default max upload size (bytes); each server can override with `--maxsize=MB` / `-m MB`. |
| `RateLimitAttempts` | 50                 | Max code checks per IP per window          |
| `RateLimitWindow`   | `10 * time.Minute` | Rate limit window                          |
| `BanDuration`       | `15 * time.Minute` | Ban duration when limit exceeded           |


### Commands


| Command              | Description                                                                              |
| -------------------- | ---------------------------------------------------------------------------------------- |
| `tcpraw server`      | Run the server (stores encrypted blobs).                                                 |
| `tcpraw servers`     | Test each server separately: ping, free space, 2s download and 2s upload of random data (random-file simulation); results table. |
| `tcpraw send`        | Upload a file; generates 6-digit code, encrypts, prints code. Option `--server` / `-s`.  |
| `tcpraw secure send` | Upload with your own 256-bit key; server assigns code. Option `--server` / `-s`.         |
| `tcpraw get`         | Download by 6-digit code (decrypts; for secure uploads, provide key).                    |
| `tcpraw <file> -l`   | Shortcut: send a file on the local LAN without a server (`--local` also works).          |
| `tcpraw get -l`      | Receive a file on the local LAN without a server (`--local` also works).                 |

Every option has a short form (`-x`) and a long form (`--name`). Every command supports `-h` and `--help` (exit code 0). Running with no arguments or an unknown command shows help (exit code 1).

**Exit codes:** `0` – success, `1` – usage error (bad arguments), `2` – runtime error.


### Arguments and options


| Command              | Argument / option | Description                                            |
| -------------------- | ----------------- | ------------------------------------------------------ |
| `tcpraw server`      | `-i` / `--id`        | Server id (first digit of generated codes); default 0. |
| `tcpraw server`      | `-p` / `--port`      | TCP port (default 9999).                               |
| `tcpraw server`      | `-d` / `--dir`       | Directory for blobs (default `./data`).                |
| `tcpraw server`      | `-w` / `--web`       | HTTP port for browser download page; omit = disabled.  |
| `tcpraw server`      | `-m` / `--maxsize`   | Per-server max upload size in MB (0 = default from code). Each instance can have a different limit. |
| `tcpraw server`      | `-t` / `--longterm`  | Enable long-term storage (client `--longterm=7d`; max 150 MB).                         |
| `tcpraw send`        | `-s` / `--server`    | Use server with that id from list (default: auto).                                     |
| `tcpraw send`        | `-t` / `--longterm`  | Store for e.g. 7 days (max 150 MB; server must have `--longterm`).                     |
| `tcpraw send`        | `-z` / `--zip`    | Pack file or directory into tar.gz before sending.                                     |
| `tcpraw send`        | `-l` / `--local`  | Local LAN mode (no server).                                                            |
| `tcpraw send`        | `<file>`          | Path to file to upload.                                                                |
| `tcpraw send`        | `[host:port]`     | Optional: server address (overrides list).             |
| `tcpraw secure send` | `-s` / `--server` | Use server with that id from list (default: auto).                                     |
| `tcpraw secure send` | `-t` / `--longterm` | Store for e.g. 7 days (max 150 MB; server must have `--longterm`).                   |
| `tcpraw secure send` | `-z` / `--zip`    | Pack file or directory into tar.gz before sending.                                     |
| `tcpraw secure send` | `<file>`          | Path to file to upload.                                                                |
| `tcpraw secure send` | `[host:port]`     | Optional: server address (overrides list).             |
| `tcpraw get`         | `<6-digit-code>`  | Code returned when uploading.                                                          |
| `tcpraw get`         | `-o` / `--output` | Output filename (default: from server).                                                |
| `tcpraw get`         | `-u` / `--unzip`  | After download, extract tar.gz and remove archive.                                     |
| `tcpraw get`         | `-l` / `--local`  | Local LAN receive mode (no server).                                                    |


### Usage

**Server:**

```bash
tcpraw server -p 9999 -d ./data
```

With web download page (browser, no client):

```bash
tcpraw server --port 9999 --dir ./data --web 8080
```

Then open `http://SERVER:8080` and enter the 6-digit code to download.

- **-i / --id** – Server id 0–9 (first digit of generated codes); default 0.
- **-p / --port** – TCP port for the tcpraw protocol (default 9999).
- **-d / --dir** – Directory for stored encrypted blobs (default `./data`).
- **-w / --web** – HTTP port for the download page; omit to disable.
- **-m / --maxsize** – Per-server max upload size in MB (0 = default from code). Each server instance can have a different limit.
- **-t / --longterm** – Enable long-term storage: client may send with `--longterm=7d` or `-t 24h` (max 150 MB).

Data is stored on disk. On startup, orphan and expired blobs are removed.

**Send (upload):**

```bash
tcpraw send [-s 0..9|--server=0..9] [-t 7d|--longterm=7d] [-z|--zip] [-l|--local] <file> [host:port]
```

Uploads the file, encrypts it with a new 6-digit code, and prints the code. Use `-z` or `--zip` to pack a file or directory into tar.gz. Use `-l` or `--local` for LAN transfer without a server. Option `--server=0..9` / `-s 0..9` picks server from list (default: auto). Option `--longterm=7d` / `-t 7d` stores the file for the given duration instead of 30 minutes – max 150 MB, server must be started with `--longterm` / `-t`. Optionally `host:port` overrides the address.

**Secure send (upload with your own key):**

```bash
tcpraw secure send [-s 0..9|--server=0..9] [-t 7d|--longterm=7d] [-z|--zip] <file> [host:port]
```

Encrypts the file with a 256-bit key (generated by the client). The server assigns the 6-digit code and stores data encrypted; **it never sees the key**. After upload you get the code and the key (64 hex chars) – without the key the file cannot be decrypted. For files >500 MB data is streamed (no more than ~500 MB in RAM). Option `--longterm=7d` / `-t 7d` – max 150 MB, server must have `--longterm` / `-t`.

**Get (download):**

```bash
tcpraw get <6-digit-code> [-o file|--output file] [-u|--unzip]
tcpraw get -l
```

Downloads the file for the given code. Flags may appear before or after the code. For regular “send” uploads decryption uses the code. For “secure send” the program will prompt for the key (64 hex chars). Optional `-o` / `--output` sets the output filename. Use `-u` / `--unzip` to extract a tar.gz after download.

### Protocol summary

- **Upload (send):** Client sends type `U`, 6-byte code, encrypted payload (name, checksum, nonce, sealed). Server stores by code and responds. Large files may use chunked format.
- **Upload (secure send):** Client sends type `S`; format 0 = single blob (file ≤500 MB in RAM), format 1 = chunked (file >500 MB, streamed). Server stores encrypted, generates code, returns code; never sees the key.
- **Download:** Client sends type `D` and 6-byte code. Server returns status and format byte (0 = single blob, 1 = chunked regular, 2 = secure single, 3 = secure chunked). Client decrypts (by code or key) and verifies checksum.
- **Web:** GET `/` shows a form; GET `/get?code=XXXXXX` returns the file as attachment only for regular uploads (server decrypts by code). “Secure send” files require the client and the key.

### Rate limiting

- Each attempt to download (TCP or web) by code counts per IP.
- Default: 50 attempts per 10 minutes per IP; then that IP is banned for 15 minutes.
- Applies to both the TCP protocol and the web download page.

### License

Use and modify as you like.