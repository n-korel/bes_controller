# Взаимодействие БУЦИС ↔ БЭС (Сармат)

Документ описывает **только** обмен между:

- **БУЦИС** — головной блок управления (кабина машиниста)
- **БЭС** — блок экстренной связи в вагоне (кнопка/индикация + голос)

## Сеть и транспорт

- **Подсеть**: `192.168.5.0/24`
- **UDP**: регистрация/keepalive и служебные события
- **SIP/RTP**: голосовая связь (сигнализация/медиа)

## UDP-обмен (БУЦИС ↔ БЭС)

### Порты

| Порт | Протокол | Назначение |
|---:|---|---|
| 6710 | UDP | Запрос регистрации от БЭС к БУЦИС (`ClientQuery`) |
| 8890 | UDP | Регистрация/keepalive/события разговора (БУЦИС ↔ БЭС) |

### Пакеты

| Пакет | Порт | Направление | Назначение |
|---|---:|---|---|
| `ClientReset` | 8890/UDP | БУЦИС → БЭС | Старт регистрации, передача IP голов (IP_BUCIS1, IP_BUCIS2) |
| `ClientQuery` | 6710/UDP | БЭС → БУЦИС | Запрос регистрации (MAC + факт нажатия) |
| `ClientAnswer` | 8890/UDP | БУЦИС → БЭС | Ответ регистрации: `sipId`, `newIp` |
| `KeepAlive` | 8890/UDP | БУЦИС → БЭС | Heartbeat: IP активного SIP-сервера |
| `ClientConversation` | 8890/UDP | БУЦИС → БЭС | Событие завершения разговора по `sipId` |

### Регистрация БЭС

```mermaid
sequenceDiagram
    participant B as БУЦИС
    participant E as БЭС

    B->>E: ClientReset (UDP 8890) IP_BUCIS1, IP_BUCIS2
    Note over E: Пассажир нажал кнопку
    E->>B: ClientQuery (UDP 6710) MAC
    B->>E: ClientAnswer (UDP 8890) sipId, newIp
    Note over E: Создание *.bind, SIP REGISTER
```

### KeepAlive (обновление IP SIP-сервера)

```mermaid
sequenceDiagram
    participant B as БУЦИС
    participant E as БЭС

    loop Каждые N секунд
        B->>E: KeepAlive (UDP 8890) opensipsIp
        alt opensipsIp изменился
            E->>E: Перечитать bind-конфиг
            E->>E: Перерегистрироваться (SIP REGISTER)
        end
    end
```

## Голос (SIP/RTP)

### Порты

| Порт | Протокол | Назначение |
|---:|---|---|
| 5060 | SIP/UDP | Сигнализация (REGISTER/INVITE/BYE) |
| 5003–5009 | RTP/UDP | Аудио (G.726), дуплекс |

### Сценарий вызова «пассажир → машинист»

```mermaid
sequenceDiagram
    autonumber
    participant E as БЭС
    participant OS as OpenSIPS (на БУЦИС :5060)
    participant B as БУЦИС

    E->>OS: SIP REGISTER
    OS-->>E: 200 OK

    E->>OS: SIP INVITE (машинист)
    OS->>B: SIP INVITE
    B-->>OS: 200 OK + SDP
    OS-->>E: 200 OK + SDP
    E->>OS: SIP ACK

    Note over E,B: RTP G.726 (5003–5009) — напрямую между абонентами
    E->>B: RTP (дуплекс)
    B->>E: RTP (дуплекс)

    E->>OS: SIP BYE
    OS->>B: SIP BYE
```
