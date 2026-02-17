# tcpraw

## 🇵🇱Polski

Przesyłanie plików przez TCP z 6-cyfrowymi kodami. Kod generuje klient i szyfruje plik; serwer przechowuje dane w postaci zaszyfrowanej. Bez rejestracji.

### Funkcje

- **Kod generuje klient** – 6-cyfrowy kod powstaje na Twoim komputerze; serwer nie zna klucza.
- **Szyfrowanie** – Dane są szyfrowane (AES-256-GCM) kluczem z kodu przed wysłaniem. Przechowywane i przesyłane w formie zaszyfrowanej.
- **Checksum** – Weryfikacja SHA256 przy wysyłce i pobieraniu.
- **Pobieranie w przeglądarce** – Opcjonalna strona HTTP: otwórz w przeglądarce, wpisz kod, pobierz bez instalacji klienta.
- **Limit prób** – Limit sprawdzeń kodu na IP (domyślnie 50 na 10 min); przekroczenie = ban 15 minut.
- **Konfiguracja** – Domyślny adres serwera, czas przechowywania, odstępy czyszczenia, max rozmiar uploadu i limity ustawiasz w `main.go`.

### Wymagania

- Go 1.21+

### Instalacja (Linux)

```bash
curl -sSL https://raw.githubusercontent.com/hdmain/rawuploader/main/install.sh | bash
```

### Kompilacja

```bash
go build -o tcpraw .
```

### Konfiguracja (main.go)

Domyślne wartości zmieniasz w zmiennych na początku pliku `main.go`:

| Zmienna              | Domyślnie             | Opis                                      |
|----------------------|------------------------|-------------------------------------------|
| `DefaultServerAddr`  | `94.249.197.155:9999` | Domyślny serwer dla send/get              |
| `StorageDuration`    | `30 * time.Minute`    | Jak długo przechowywane są dane           |
| `CleanupInterval`    | `5 * time.Minute`     | Co ile usuwane są wygasłe bloby           |
| `MaxBlobSize`        | 4 GB                  | Maks. rozmiar jednego uploadu (bajty)     |
| `RateLimitAttempts`  | 50                    | Maks. sprawdzeń kodu na IP w oknie        |
| `RateLimitWindow`    | `10 * time.Minute`    | Okno czasowe limitu                        |
| `BanDuration`        | `15 * time.Minute`    | Czas bana po przekroczeniu limitu          |

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

- **-port** – Port TCP protokołu tcpraw (domyślnie 9999).
- **-dir** – Katalog na zaszyfrowane bloby (domyślnie `./data`).
- **-web** – Port HTTP strony pobierania; pomiń, żeby wyłączyć.

Dane są zapisywane na dysku. Przy starcie usuwane są stare i wygasłe bloby.

**Send (wysyłanie):**

```bash
tcpraw send <plik> [host:port]
```

Wysyła plik, szyfruje go nowym 6-cyfrowym kodem i wypisuje kod. Gdy nie podasz `host:port`, używany jest `DefaultServerAddr` z `main.go` (z fallbackiem z URL przy timeoutcie połączenia).

**Get (pobieranie):**

```bash
tcpraw get <6-cyfrowy-kod> [host:port] [-o plik]
```

Pobiera plik po podanym kodzie i odszyfrowuje. Opcja `-o` ustawia nazwę zapisanego pliku.

### Protokół w skrócie

- **Upload:** Klient wysyła typ `U`, potem 6-bajtowy kod, potem zaszyfrowane dane (nazwa, checksum, nonce, sealed). Serwer zapisuje pod kodem i zwraca status.
- **Download:** Klient wysyła typ `D` i 6-bajtowy kod. Serwer zwraca status i – jeśli jest – zaszyfrowany blob; klient odszyfrowuje i sprawdza checksum.
- **Web:** GET `/` pokazuje formularz; GET `/get?code=XXXXXX` zwraca plik jako załącznik (serwer odszyfrowuje używając kodu z żądania).

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
- **Checksum** – SHA256 verification on upload and download.
- **Web download** – Optional HTTP page: open in a browser, enter the code, download without installing the client.
- **Rate limiting** – Per-IP limit on code checks (default 50 per 10 min); excess leads to a 15-minute ban.
- **Configurable** – Default server address, storage duration, cleanup interval, max upload size, and rate limits are set in `main.go`.

### Requirements

- Go 1.21+

### Installation (Linux)

```bash
curl -sSL https://raw.githubusercontent.com/hdmain/rawuploader/main/install.sh | bash
```

### Build

```bash
go build -o tcpraw .
```

### Configuration (main.go)

Edit the variables at the top of `main.go` to change defaults:

| Variable             | Default              | Description                          |
|----------------------|----------------------|--------------------------------------|
| `DefaultServerAddr`  | `94.249.197.155:9999`| Default server for send/get          |
| `StorageDuration`    | `30 * time.Minute`   | How long blobs are kept              |
| `CleanupInterval`    | `5 * time.Minute`    | How often expired blobs are removed  |
| `MaxBlobSize`        | 4 GB                 | Max size per upload (bytes)          |
| `RateLimitAttempts`  | 50                   | Max code checks per IP per window    |
| `RateLimitWindow`    | `10 * time.Minute`   | Rate limit window                    |
| `BanDuration`        | `15 * time.Minute`   | Ban duration when limit exceeded     |

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

- **-port** – TCP port for the tcpraw protocol (default 9999).
- **-dir** – Directory for stored encrypted blobs (default `./data`).
- **-web** – HTTP port for the download page; omit to disable.

Data is stored on disk. On startup, orphan and expired blobs are removed.

**Send (upload):**

```bash
tcpraw send <file> [host:port]
```

Uploads the file, encrypts it with a new 6-digit code, and prints the code. If `host:port` is omitted, `DefaultServerAddr` from `main.go` is used (with fallback from a URL on connection timeout).

**Get (download):**

```bash
tcpraw get <6-digit-code> [host:port] [-o file]
```

Downloads the file for the given code and decrypts it. Optional `-o` sets the output filename.

### Protocol summary

- **Upload:** Client sends message type `U`, then 6-byte code, then encrypted payload (name, checksum, nonce, sealed data). Server stores by code and responds with status.
- **Download:** Client sends message type `D` and 6-byte code. Server returns status and, if found, the encrypted blob; client decrypts and verifies checksum.
- **Web:** GET `/` shows a form; GET `/get?code=XXXXXX` returns the file as attachment (server decrypts using the code from the request).

### Rate limiting

- Each attempt to download (TCP or web) by code counts per IP.
- Default: 50 attempts per 10 minutes per IP; then that IP is banned for 15 minutes.
- Applies to both the TCP protocol and the web download page.

### License

Use and modify as you like.
