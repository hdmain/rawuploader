# tcpraw

Przesyłanie plików przez TCP z 6-cyfrowymi kodami. Kod generuje klient i szyfruje plik; serwer przechowuje dane w postaci zaszyfrowanej. Bez rejestracji.

## Funkcje

- **Kod generuje klient** – 6-cyfrowy kod powstaje na Twoim komputerze; serwer nie zna klucza.
- **Szyfrowanie** – Dane są szyfrowane (AES-256-GCM) kluczem z kodu przed wysłaniem. Przechowywane i przesyłane w formie zaszyfrowanej.
- **Checksum** – Weryfikacja SHA256 przy wysyłce i pobieraniu.
- **Pobieranie w przeglądarce** – Opcjonalna strona HTTP: otwórz w przeglądarce, wpisz kod, pobierz bez instalacji klienta.
- **Limit prób** – Limit sprawdzeń kodu na IP (domyślnie 50 na 10 min); przekroczenie = ban 15 minut.
- **Konfiguracja** – Domyślny adres serwera, czas przechowywania, odstępy czyszczenia, max rozmiar uploadu i limity ustawiasz w `main.go`.

## Wymagania

- Go 1.21+

## Kompilacja

```bash
go build -o tcpraw .
```

## Konfiguracja (main.go)

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

## Użycie

### Serwer

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

### Send (wysyłanie)

```bash
tcpraw send <plik> [host:port]
```

Wysyła plik, szyfruje go nowym 6-cyfrowym kodem i wypisuje kod. Gdy nie podasz `host:port`, używany jest `DefaultServerAddr` z `main.go` (z fallbackiem z URL przy timeoutcie połączenia).

Przykład:

```bash
tcpraw send dokument.pdf
# File sent (encrypted). Your code: 482917 (valid 30 min)
```

### Get (pobieranie)

```bash
tcpraw get <6-cyfrowy-kod> [host:port] [-o plik]
```

Pobiera plik po podanym kodzie i odszyfrowuje. Opcja `-o` ustawia nazwę zapisanego pliku.

Przykład:

```bash
tcpraw get 482917 -o moj_plik.pdf
```

## Protokół w skrócie

- **Upload:** Klient wysyła typ `U`, potem 6-bajtowy kod, potem zaszyfrowane dane (nazwa, checksum, nonce, sealed). Serwer zapisuje pod kodem i zwraca status.
- **Download:** Klient wysyła typ `D` i 6-bajtowy kod. Serwer zwraca status i – jeśli jest – zaszyfrowany blob; klient odszyfrowuje i sprawdza checksum.
- **Web:** GET `/` pokazuje formularz; GET `/get?code=XXXXXX` zwraca plik jako załącznik (serwer odszyfrowuje używając kodu z żądania).

## Limit prób (rate limiting)

- Każda próba pobrania po kodzie (TCP lub strona) jest liczona per IP.
- Domyślnie: 50 prób na 10 minut na IP; potem ten IP jest zbanowany na 15 minut.
- Dotyczy zarówno protokołu TCP, jak i strony do pobierania.

## Licencja

Możesz używać i modyfikować dowolnie.
