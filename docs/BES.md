# BES28 — описание проекта

Документ описывает структуру, назначение и работу исходного кода ПО БЭС-28 (блок абонентский связи) в составе системы Сармат (БЭС-М25 САЕШ.465489.129, ЦИК Москва).

---

## 1. Назначение

**BES28** — программный комплекс для абонентского блока связи в поезде. Обеспечивает:

- **Голосовую связь по SIP** между блоками через сервер OpenSIPS на головном устройстве (БУЦИК).
- **Регистрацию блока** в составе поезда (привязка к конкретному БУЦИК по нажатию кнопки и обмену по UDP).
- **Индикацию состояний** (светодиоды: ожидание, регистрация, разговор, очередь и т.д.).
- **Обработку кнопки** на блоке (звонок, ответ, завершение).
- **Воспроизведение звуков** (при ответе, занято, окончание разговора).
- **Тестирование** микрофона/динамика (анализ частоты через FFT) и тестовый режим с ПК (Tester).

Режим работы задаётся конфигурацией: блок может работать как **БЭС** (абонентский блок в вагоне) или как **БУЦИК** (головное устройство в голове поезда).

---

## 2. Состав репозитория (модули)

| Модуль | Назначение |
|--------|------------|
| **Manager** | Основное приложение: логика БЭС/БУЦИК, SIP-клиент, телефония, RTP, настройки, D-Bus-клиенты. Устанавливается в `/opt/Manager/bin`. |
| **UdpHandler** | Служба приёма/отправки UDP: протокол с БУЦИК (регистрация, keepalive, команды теста). Публикует события в D-Bus. |
| **LedHandler** | Служба управления светодиодами через GPIO. Реализует состояния (ожидание, регистрация, разговор, очередь и т.д.) через конечный автомат. |
| **ButtonHandler** | Служба опроса кнопки по GPIO (poll), эмуляция нажатия. Публикует сигнал нажатия в D-Bus. |
| **Player** | Тестовая/доп. программа: воспроизведение/запись по командам от Tester (300/1000/3200 Гц, запись, LED-тест). |
| **GpioLib** | Библиотека работы с GPIO через sysfs (`/sys/class/gpio`): экспорт, направление, чтение/запись value, edge для прерываний. |
| **PwmLib** | Библиотека работы с PWM через sysfs (`/sys/class/pwm/pwmchip1`): экспорт, duty/period, enable. |
| **Tester** | Desktop-приложение (Qt) для тестирования БЭС с ПК по сети. |
| **BusicTester** | Утилита тестирования в режиме БУЦИК (подключение к BUSIC.Events по D-Bus). |
| **libs/sofia_arm** | Сборка библиотеки Sofia-SIP (ARM) для SIP-стека (NUA, регистрация, INVITE, BYE и т.д.). |

Все взаимодействия между процессами — через **D-Bus (system bus)** и **UDP** по локальной сети 192.168.5.0/24.

---

## 3. Процессы и D-Bus

Одновременно на устройстве работают несколько процессов:

| Процесс | D-Bus сервис | Роль |
|---------|----------------|------|
| **Manager** | Подключается к другим; при режиме Busic регистрирует `BES28.BusicInformator` | Логика БЭС/БУЦИК, SIP, RTP, настройки |
| **UdpHandler** | `BES28.UdpHandler`, объект `/BES28` | Приём UDP, парсинг пакетов, сигналы: startRegistration, sipId, opensipsIp, stopTimer, startTestBes, play300/1000/3200, TestLedOn/Off/Blink, recordSig, stopSig и др. |
| **LedHandler** | `BES28.LedHandler`, `/BES28` | Включение/выключение LED по состояниям (ожидание, регистрация, зарегистрирован, очередь, громко и т.д.) |
| **ButtonHandler** | `BES28.ButtonHandler`, `/BES28` | Сигнал `onClick` при нажатии кнопки (GPIO) |
| **Player** (тест) | `BES28.TestHandler`, `/BES28` | Сигналы таймаута/остановки теста для Tester |
| **BusicTester** | `BES28.ButtonHandler`, `BUSIC.Events` | Эмуляция кнопки и приём событий от Manager в режиме БУЦИК |

Manager не регистрирует отдельный «сервис Manager» — он только **подписывается** на сигналы и вызывает методы интерфейсов UdpHandler, LedHandler, ButtonHandler (через прокси D-Bus). Исключение: в режиме Busic регистрируется `BES28.BusicInformator` для уведомления тестов/внешних приложений о событиях SIP.

---

## 4. Режимы работы: BES и Busic

Тип устройства задаётся в **settings.ini** (флаг `isBusic`). При старте Manager читает настройки из `/opt/sarmat/` и создаёт один из двух юнитов.

### 4.1 Режим BES (абонентский блок)

- **Состояния** (`STATE_BES28`): UNREGISTERED → REGISTRATION → REGISTERED.
- При **REGISTRATION**: по нажатию кнопки UdpHandler отправляет на БУЦИК пакет «клиент нажал кнопку» (ClientQuery + MAC); БУЦИК отвечает (ClientAnswer) с sipId и новым IP; Manager создаёт bind-файлы, перезапускает сеть и UdpHandler, стартует SIP.
- При **REGISTERED**: кнопка инициирует звонок через SIP (INVITE на DEST, например 300/400 для БРС).
- По **keepalive** от БУЦИК (UDP) приходит IP активного OpenSIPS; при смене активной головы Manager переключает привязку и перезапускает SIP.
- Воспроизводятся треки: ответ (onSpeak), занято (onBusy), окончание (onEnd) — пути из `Utils::readFiles()` (например `/opt/sarmat/tracks/`).
- При старте выполняется **Analyzer**: проверка микрофона/динамика (тон 300–1000 Гц, запись, FFT), результат уходит в UdpHandler (metrics).

### 4.2 Режим Busic (головное устройство)

- Нет регистрации «в поезде»; есть понятие текущей/другой головы (IP_BUCIS1 / IP_BUCIS2).
- Кнопка напрямую передаётся в TelephoneHandler (звонок/ответ).
- События SIP (входящий, конец вызова, занято, очередь и т.д.) пробрасываются в D-Bus через **DbusBusicInformator** для внешних подписчиков (BusicTester и др.).
- При смене активной головы (DbusBusicEvents::isActiveCurrentHead) при наличии актуального bind делается restart SIP.

---

## 5. SIP и телефония

- **Стек**: Sofia-SIP (libs/sofia_arm), NUA API.
- **Сервер**: OpenSIPS на БУЦИК (один из двух поездных голов 192.168.5.251 / 192.168.5.252). При смене головы используется keepalive (UDP).
- **Привязка**: для каждого «направления» (текущий БУЦИК, второй БУЦИК, БРС 201/202) в `/opt/sarmat/` создаются файлы `*.bind` (INI с секцией [SIP]: DOMAIN, USER, URI, DEST_IP, DEST, PROXY, PORT_SIP, PORT_RTP). Активный набор выбирается по IP OpenSIPS из keepalive.
- **Режимы телефона** (`MODE_TELEPHONE`): UNDEFINED, REGISTRATION, REGISTERED, INCOMING_CALL, CALLING, ANSWERED, CALL.
- **Цепочка**: SipService → SipClient (NUA) + SipHelper; события (registered, invite, answered, hangup, busy, error и т.д.) идут в Telephone → TelephoneHandler → BES/Busic и в D-Bus (LED, BusicInformator).
- **RTP**: GStreamer (gsttransmit, gstreceive, pipeline с портом из SipSettings, например 5003).

---

## 6. UDP-протокол (UdpHandler)

- **Сеть**: 192.168.5.0/24, broadcast 192.168.5.255.
- **Порты** (в UdpHandler/proto.h): 8890 — основной обмен с БУЦИК, 8888 — тестовые команды, 6710 — ответы БУЦИК (clientReset и т.д.), 8892 — приём, 8881 — отправка метрик.
- **Типы пакетов** (парсинг в CmdHandler):  
  - **ClientReset** — старт регистрации (ip первого/второго БУЦИК) → startRegistration.  
  - **ClientAnswer** — ответ с sipId и newIp → sipId.  
  - **KeepAlive** — IP активного OpenSIPS и статус → opensipsIp.  
  - **ClientConversation** — останов таймера по sipId → stopTimer.  
  - **TestCmd** — startTestBes, stop, record, 300/1000/3200, TestLedOn/Blink/Off и др. → соответствующие сигналы D-Bus.
- При нажатии кнопки в режиме регистрации UdpHandler по сигналу D-Bus от Manager отправляет **ClientQuery** (текст + MAC) на currentActiveBusic:PORT_LISTEN_BUCIS.

---

## 7. Конфигурация и пути

- **Корень настроек**: `/opt/sarmat/` (в коде захардкожено в MainClass Manager и в SettingsService).
- **Файлы**:  
  - `settings.ini` — общие настройки приложения (в т.ч. isBusic).  
  - `*.bind` — привязки SIP (по одному на DEST_IP); читаются при старте и при создании привязки из setSipId.
- **Треки**: путь из Utils (например `/opt/sarmat/tracks/`), список файлов через `Utils::readFiles()`.
- **Установка**: `target.path = /opt/$${TARGET}/bin` (например `/opt/Manager/bin`).

---

## 8. Константы (proto.h, сеть)

- **Подсеть**: 192.168.5.0/24, broadcast 192.168.5.255.
- **БУЦИК**: 192.168.5.251 (IP_BUCIS1), 192.168.5.252 (IP_BUCIS2).
- **БРС**: 192.168.5.201 (IP_BRS1), 192.168.5.202 (IP_BRS2).
- **Типы устройств**: TYPE_DEVICE (BES, MIES, MDU, …) для UDP-сообщений и метрик.
- В **Manager/proto.h**: PORT_LISTEN 8888, PORT_LISTEN_BUCIS 7777, PORT_ANSWER 8880.  
  В **UdpHandler/proto.h** свои порты (8890, 8888, 6710, 8892, 8881) — при совместной работе нужно согласование, кто на каком порту слушает.

---

## 9. Железо и периферия

- **GPIO**: LedHandler и ButtonHandler используют GpioLib; номера пинов заданы в состояниях (например `SecoMq7_962::Led`, `SecoMq7_962::Button`), в ButtonHandler — gpio 17 для кнопки.
- **PWM**: через PwmLib (pwmchip1) — для индикации (мигание и т.д.) в состояниях LED.
- **Звук**: GStreamer, ALSA (amixer в Utils для громкости), при необходимости sndfile/fftw3 для анализа (Analyzer).

---

## 10. Сборка

- **Система сборки**: Qt qmake (`.pro` в каждом модуле).
- **Целевая платформа**: в Manager.pro указана сборка под ARM (QT_VERSION 5.15.2, sysroot cortexa9hf-neon-poky-linux-gnueabi). Подключены: glib-2.0, GStreamer, Sofia-SIP, alsa, sndfile, fftw3.
- **Зависимости**: Qt5 (Core, DBus, Network, Multimedia), системная D-Bus, GStreamer, Sofia-SIP (из libs/sofia_arm или системная).

---

## 11. Краткая схема взаимодействия (BES)

1. Загрузка → Manager читает `/opt/sarmat/settings.ini` и `*.bind`, создаёт BES или Busic.
2. Если bind уже есть — состояние REGISTERED, LED «ожидание», иначе — «регистрация».
3. Регистрация: БУЦИК шлёт по UDP ClientReset (ip_first, ip_second) → UdpHandler → D-Bus startRegistration → Manager переходит в REGISTRATION, LED «регистрация». Пользователь нажимает кнопку → ButtonHandler → D-Bus onClick → Manager шлёт через UdpHandler ClientQuery на БУЦИК; БУЦИК отвечает ClientAnswer(sipId, newIp) → UdpHandler → D-Bus sipId → Manager создаёт bind-файлы, меняет IP, перезапускает сеть и UdpHandler, запускает SIP.
4. После регистрации: keepalive от БУЦИК приносит IP активного OpenSIPS; при смене IP Manager переключает bind и перезапускает SIP.
5. В режиме «ожидание» нажатие кнопки → звонок (INVITE на DEST 300/400 или другой); при ответе/занято/отбое воспроизводятся треки и меняется LED.
6. Таймер разговора: при определённых событиях (ClientConversation) UdpHandler шлёт stopTimer(sipId); Manager завершает звонок по таймеру.

---

## 12. Важные замечания

- **Два варианта proto.h**: в Manager и UdpHandler заданы разные порты; при развёртывании нужно убедиться, что БУЦИК и тестовые скрипты используют ожидаемые порты.
- **Путь /opt/sarmat/** жёстко задан в коде; смена окружения потребует правок или вынесения в конфиг.
- **Исключения при старте**: при ошибках чтения/записи настроек или SIP Manager ловит SettingsReaderException, SettingsWriterException, SipErrorException и завершается с кодом -1.
- **Тест БЭС**: по команде startTestBes Manager делает `qApp->exit(100)`; внешний скрипт/супервизор может переключить на запуск Player (Tester). Player при получении startTestBes может вызвать exit(101) и остановить тестер (сигнал по D-Bus).

Если нужно углубиться в конкретный модуль (SIP, D-Bus-интерфейсы, форматы UDP-пакетов или форматы bind/settings.ini), можно описать их отдельно в следующих разделах этого файла.
