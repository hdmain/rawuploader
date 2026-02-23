# tcpraw

## 🇵🇱Polski

Przesyłanie plików przez TCP z 6-cyfrowymi kodami. Kod generuje klient i szyfruje plik; serwer przechowuje dane w postaci zaszyfrowanej. Bez rejestracji.

### Funkcje

- **Kod generuje klient** – 6-cyfrowy kod powstaje na Twoim komputerze; serwer nie zna klucza.
- **Szyfrowanie** – Dane są szyfrowane (AES-256-GCM) kluczem z kodu przed wysłaniem. Przechowywane i przesyłane w formie zaszyfrowanej.
- **Secure send** – Tryb z własnym kluczem 256-bit: serwer przypisuje kod, klient trzyma klucz; pobieranie tylko z klientem i podaniem klucza. Pliki &gt;500 MB są strumieniowane (max 500 MB w RAM).
- **Checksum** – Weryfikacja SHA256 przy wysyłce i pobieraniu.
- **Pobieranie w przeglądarce** – Opcjonalna strona HTTP: otwórz w przeglądarce, wpisz kod, pobierz bez instalacji klienta (tylko uploady zwykłe „send”; secure wymaga klienta i klucza).
- **Limit prób** – Limit sprawdzeń kodu na IP (domyślnie 50 na 10 min); przekroczenie = ban 15 minut.
- **Total network storage** – Uruchomienie `tcpraw` (bez argumentów lub z nieznaną komendą) pokazuje łączne wolne miejsce na wszystkich serwerach z listy.
- **Konfiguracja** – Czas przechowywania, odstępy czyszczenia, max rozmiar uploadu i limity ustawiasz w `main.go`; lista serwerów w kodzie (pierwsza cyfra kodu = id serwera).

### Wymagania

- Go 1.21+

### Instalacja (Linux)

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

| Zmienna              | Domyślnie             | Opis                                      |
|----------------------|------------------------|-------------------------------------------|
| (lista serwerów)     | w kodzie `client.go`  | Adresy serwerów; pierwsza cyfra kodu = id |
| `StorageDuration`    | `30 * time.Minute`    | Jak długo przechowywane są dane           |
| `CleanupInterval`    | `5 * time.Minute`     | Co ile usuwane są wygasłe bloby           |
| `MaxBlobSize`        | 15 GB                 | Maks. rozmiar jednego uploadu (bajty)     |
| `RateLimitAttempts`  | 50                    | Maks. sprawdzeń kodu na IP w oknie        |
| `RateLimitWindow`    | `10 * time.Minute`    | Okno czasowe limitu                        |
| `BanDuration`        | `15 * time.Minute`    | Czas bana po przekroczeniu limitu          |

### Komendy (tabela)

| Komenda | Opis | Opcje / argumenty |
|--------|------|--------------------|
| `tcpraw server` | Uruchamia serwer (przechowuje zaszyfrowane bloby). | `-id=0..9` id serwera (pierwsza cyfra kodu), `-port=9999`, `-dir=./data`, `-web=PORT` strona pobierania w przeglądarce, `-maxsize=MB` max rozmiar uploadu (0 = domyślny). |
| `tcpraw send` | Wysyła plik; generuje 6-cyfrowy kod, szyfruje, wypisuje kod. | `-server=0..9` wybór serwera z listy (domyślnie: auto), `<plik>`, opcjonalnie `[host:port]`. |
| `tcpraw secure send` | Wysyła plik z własnym kluczem 256-bit; serwer przypisuje kod. | `-server=0..9` wybór serwera, `<plik>`, opcjonalnie `[host:port]`. |
| `tcpraw get` | Pobiera plik po 6-cyfrowym kodzie (odszyfrowuje; dla secure – podaj klucz). | `<6-cyfrowy-kod>`, `-o plik` nazwa zapisanego pliku. |

### Użycie

**Serwer:**

```bash
tcpraw server -port=9999 -dir=./data
```

Z włączoną stroną do pobierania w przeglądarce (bez klienta):

```bash
tcpraw server -port=9999 -dir=./data -web=8080
```

Następnie otwórz `http://SERVER:8080` i wpisz 6-cyfrowy kod, żeby pobrać plik.

- **-id** – Id serwera 0–9 (pierwsza cyfra generowanych kodów); domyślnie 0.
- **-port** – Port TCP protokołu tcpraw (domyślnie 9999).
- **-dir** – Katalog na zaszyfrowane bloby (domyślnie `./data`).
- **-web** – Port HTTP strony pobierania; pomiń, żeby wyłączyć.
- **-maxsize** – Maks. rozmiar uploadu w MB (0 = domyślna wartość z kodu).

Dane są zapisywane na dysku. Przy starcie usuwane są stare i wygasłe bloby.

**Send (wysyłanie):**

```bash
tcpraw send <plik> [host:port]
```

Wysyła plik, szyfruje go nowym 6-cyfrowym kodem i wypisuje kod. Serwer jest wybierany z listy adresów (pierwsza cyfra kodu = id serwera). Opcjonalnie `host:port` nadpisuje adres.

**Secure send (wysyłanie z własnym kluczem):**

```bash
tcpraw secure send <plik> [host:port]
```

Szyfruje plik 256-bitowym kluczem (generowanym przez klienta). Serwer przypisuje 6-cyfrowy kod i przechowuje dane zaszyfrowane; **klucza nie zna**. Po uploadzie dostajesz kod i klucz (64 znaki hex) – bez klucza pliku nie da się odszyfrować. Dla plików &gt;500 MB dane są strumieniowane (w RAM nie więcej niż ~500 MB).

**Get (pobieranie):**

```bash
tcpraw get <6-cyfrowy-kod> [-o plik]
```

Pobiera plik po podanym kodzie. Dla uploadu zwykłego „send” odszyfrowanie jest po kodzie. Dla „secure send” program poprosi o klucz (64 znaki hex). Opcja `-o` ustawia nazwę zapisanego pliku.

### Protokół w skrócie

- **Upload (send):** Klient wysyła typ `U`, 6-bajtowy kod, zaszyfrowane dane (nazwa, checksum, nonce, sealed). Serwer zapisuje pod kodem i zwraca status. Duże pliki mogą być wysyłane chunkami (format chunked).
- **Upload (secure send):** Klient wysyła typ `S`; format 0 = jeden blob (plik ≤500 MB w RAM), format 1 = chunked (plik &gt;500 MB, strumieniowo). Serwer zapisuje zaszyfrowane dane, generuje kod, zwraca kod; klucza nie zna.
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
- **Secure send** – Mode with your own 256-bit key: server assigns the code, client keeps the key; download only with the client and the key. Files &gt;500 MB are streamed (max 500 MB in RAM).
- **Checksum** – SHA256 verification on upload and download.
- **Web download** – Optional HTTP page: open in a browser, enter the code, download without installing the client (only for regular “send” uploads; secure uploads require the client and the key).
- **Rate limiting** – Per-IP limit on code checks (default 50 per 10 min); excess leads to a 15-minute ban.
- **Total network storage** – Running `tcpraw` with no arguments or an unknown command shows total free space across all servers from the list.
- **Configurable** – Storage duration, cleanup interval, max upload size, and rate limits are set in `main.go`; server list is in code (first digit of code = server id).

### Requirements

- Go 1.21+

### Installation (Linux)

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

| Variable             | Default              | Description                          |
|----------------------|----------------------|--------------------------------------|
| (server list)        | in `client.go`       | Server addresses; first digit of code = id |
| `StorageDuration`    | `30 * time.Minute`   | How long blobs are kept              |
| `CleanupInterval`    | `5 * time.Minute`    | How often expired blobs are removed  |
| `MaxBlobSize`        | 15 GB                | Max size per upload (bytes)          |
| `RateLimitAttempts`  | 50                   | Max code checks per IP per window    |
| `RateLimitWindow`    | `10 * time.Minute`   | Rate limit window                    |
| `BanDuration`        | `15 * time.Minute`   | Ban duration when limit exceeded     |

### Commands (table)

| Command | Description | Options / arguments |
|--------|-------------|---------------------|
| `tcpraw server` | Run the server (stores encrypted blobs). | `-id=0..9` server id (first digit of code), `-port=9999`, `-dir=./data`, `-web=PORT` browser download page, `-maxsize=MB` max upload size (0 = default). |
| `tcpraw send` | Upload a file; generates 6-digit code, encrypts, prints code. | `-server=0..9` pick server from list (default: auto), `<file>`, optional `[host:port]`. |
| `tcpraw secure send` | Upload with your own 256-bit key; server assigns code. | `-server=0..9` pick server, `<file>`, optional `[host:port]`. |
| `tcpraw get` | Download by 6-digit code (decrypts; for secure uploads, provide key). | `<6-digit-code>`, `-o file` output filename. |

### Usage

**Server:**

```bash
tcpraw server -port=9999 -dir=./data
```

With web download page (browser, no client):

```bash
tcpraw server -port=9999 -dir=./data -web=8080
```

Then open `http://SERVER:8080` and enter the 6-digit code to download.

- **-id** – Server id 0–9 (first digit of generated codes); default 0.
- **-port** – TCP port for the tcpraw protocol (default 9999).
- **-dir** – Directory for stored encrypted blobs (default `./data`).
- **-web** – HTTP port for the download page; omit to disable.
- **-maxsize** – Max upload size in MB (0 = default from code).

Data is stored on disk. On startup, orphan and expired blobs are removed.

**Send (upload):**

```bash
tcpraw send <file> [host:port]
```

Uploads the file, encrypts it with a new 6-digit code, and prints the code. Server is chosen from the address list (first digit of code = server id). Optionally `host:port` overrides the address.

**Secure send (upload with your own key):**

```bash
tcpraw secure send <file> [host:port]
```

Encrypts the file with a 256-bit key (generated by the client). The server assigns the 6-digit code and stores data encrypted; **it never sees the key**. After upload you get the code and the key (64 hex chars) – without the key the file cannot be decrypted. For files &gt;500 MB data is streamed (no more than ~500 MB in RAM).

**Get (download):**

```bash
tcpraw get <6-digit-code> [-o file]
```

Downloads the file for the given code. For regular “send” uploads decryption uses the code. For “secure send” the program will prompt for the key (64 hex chars). Optional `-o` sets the output filename.

### Protocol summary

- **Upload (send):** Client sends type `U`, 6-byte code, encrypted payload (name, checksum, nonce, sealed). Server stores by code and responds. Large files may use chunked format.
- **Upload (secure send):** Client sends type `S`; format 0 = single blob (file ≤500 MB in RAM), format 1 = chunked (file &gt;500 MB, streamed). Server stores encrypted, generates code, returns code; never sees the key.
- **Download:** Client sends type `D` and 6-byte code. Server returns status and format byte (0 = single blob, 1 = chunked regular, 2 = secure single, 3 = secure chunked). Client decrypts (by code or key) and verifies checksum.
- **Web:** GET `/` shows a form; GET `/get?code=XXXXXX` returns the file as attachment only for regular uploads (server decrypts by code). “Secure send” files require the client and the key.
  
### Rate limiting

- Each attempt to download (TCP or web) by code counts per IP.
- Default: 50 attempts per 10 minutes per IP; then that IP is banned for 15 minutes.
- Applies to both the TCP protocol and the web download page.

### License

Use and modify as you like.
