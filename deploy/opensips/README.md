# OpenSIPS для симулятора БУЦИС–БЭС

Минимальный сценарий для **итерации B**: реальный SIP REGISTER/INVITE через OpenSIPS (UDP/5060).

## Запуск (Linux, OpenSIPS из пакетов)

- **установить**: `opensips` (пакет `opensips-sqlite-module` не нужен)
- **запустить** OpenSIPS с конфигом `deploy/opensips/opensips.cfg`:
  - слушает `127.0.0.1:5060/UDP` (локальный сценарий симулятора)
  - принимает REGISTER и сохраняет контакты в `location`
  - маршрутизирует INVITE по `lookup(location)`

Перед запуском убедитесь, что старые инстансы OpenSIPS не висят (иначе можно получить конфликт/непредсказуемую маршрутизацию):

- `sudo pkill -f "opensips -f deploy/opensips/opensips.cfg"`

## Переменные окружения симулятора (SIP)

На обеих ролях:

- `SIP_PORT=5060`
- `SIP_USER_BUCIS=bucis`
- `SIP_USER_BES=bes_1` (или любой другой логин)

Пароли сейчас **не обязательны**, если вы используете `deploy/opensips/opensips.cfg` без аутентификации.

