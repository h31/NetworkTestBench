# Использование

1. Скачайте [последний релиз](https://github.com/h31/NetworkTestBench/releases) и распакуйте его в папку с клиентом.
2. В файле `settings.yaml` укажите свои параметры в соответствии с комментариями из файла.
3. Запустите `./NetworkTestBench collect`. После этой команды загрузится ваш клиент и будет работать как обычно, при этом всё, что вы вводите в консоль, будет запоминаться. Введите несколько команд, как если бы вы пользовались клиентом.
5. Запустите `./NetworkTestBench test`. В ходе тестирования ваш клиент будет запущен несколько раз с разными настройками симуляции реальной сети. В качестве ввода для клиента будут использоваться ранее записанные команды.

Тесты с `DoReset:true` принудительно разрывают соединение между клиентом и серверов. Для таких тестов нормально, если клиент и сервер выдают ошибку. Ненормально, если клиент или сервер падает в ходе теста.
Тесты с `DoReset:false` вносят задержку передачи. Для таких тестов клиент и сервер должны работать нормально.

Перезапустить один конкретный тест можно с помощью команды `./NetworkTestBench -t NUM test`, где NUM - номер теста.