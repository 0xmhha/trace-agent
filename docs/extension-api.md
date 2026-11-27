# 확장 이벤트 API

플러그인, MCP 서버, 셸 스크립트를 만드는 사람을 위한 문서다. 도구 안에서 일어난 일을 Trace Agent 로 보내는 방법을 적는다.

## 1. 무엇을 위한 것인가

Claude Code 의 세션 기록에는 도구를 불렀다는 사실과 그 결과만 남는다. 도구가 안에서 무엇을 했는지는 남지 않는다. 배포 스크립트가 어느 단계에서 멈췄는지, MCP 서버가 뒤쪽 API 를 몇 번 다시 불렀는지는 세션 기록만으로 알 수 없다.

확장 이벤트 API 는 그 빈 곳을 채운다. 도구가 이벤트를 보내면 Trace Agent 가 저장하고, 세션과 도구 호출에 연결해 타임라인에 함께 보여 준다.

- 받는 곳은 이 Mac 의 `127.0.0.1` 하나뿐이다. 이벤트는 밖으로 나가지 않는다.
- 보내는 방법은 둘이다. HTTP 로 직접 보내거나 `trace-emit` 명령을 쓴다.
- 추적은 덤이다. 수신기가 꺼져 있어도 도구는 그대로 동작해야 한다. 이 문서의 예제는 모두 그 원칙을 따른다.

## 2. 켜기와 설정 복사

수신기는 기본으로 꺼져 있다.

1. 앱의 `설정 → 확장 이벤트` 에서 수신기를 켠다. 앱이 확인 창을 띄운다.
2. 같은 화면에서 설정을 복사한다. 복사한 내용에 주소와 자격이 들어 있다.
3. 두 값을 도구가 실행되는 환경의 환경 변수로 둔다.

| 환경 변수 | 내용 |
|---|---|
| `TRACE_EVENTS_URL` | 수신 주소. `http://127.0.0.1:<포트>/v1/events` 꼴이다. |
| `TRACE_EVENTS_TOKEN` | 자격. `Authorization` 헤더에 넣는다. |
| `TRACE_EVENTS_SOURCE` | 선택. `trace-emit` 이 쓰는 출처 이름의 기본값이다. |

Claude Code 의 훅과 MCP 서버는 Claude Code 를 띄운 환경을 물려받는다. 그러니 Claude Code 를 시작하는 셸에 두 값이 있어야 한다.

자격은 비밀 값이다. 저장소, 공유하는 설정 파일, 로그에 적지 않는다. 이 문서의 예제도 값을 적지 않고 `$TRACE_EVENTS_TOKEN` 으로만 가리킨다.

## 3. 요청 형식

```
POST http://127.0.0.1:<포트>/v1/events
Authorization: Bearer $TRACE_EVENTS_TOKEN
Content-Type: application/json
```

본문은 이벤트 하나이거나, 여러 개를 묶은 `{"events":[...]}` 이다.

```json
{
  "version": 1,
  "id": "<이벤트마다 새로 만든 무작위 문자열>",
  "source": "deploy-script",
  "kind": "span",
  "name": "upload",
  "timestamp": "2026-01-01T01:00:00.000Z",
  "duration_ms": 812.5,
  "status": "error",
  "level": "error",
  "session_id": "<Claude Code 세션 ID>",
  "tool_use_id": "<도구 호출 ID>",
  "message": "exit status 1",
  "attributes": {"step": "3", "retries": 2},
  "value": 1.0,
  "unit": "count"
}
```

| 필드 | 필수 | 규칙 |
|---|---|---|
| `version` | 필수 | `1`. |
| `source` | 필수 | 보내는 도구의 이름. `^[A-Za-z0-9._-]{1,64}$`. |
| `kind` | 필수 | `span`, `log`, `metric` 중 하나. |
| `name` | 필수 | 일어난 일의 이름. 1~128자. |
| `id` | 선택 | 보내는 쪽이 정하는 멱등 키. 6장을 본다. |
| `timestamp` | 선택 | RFC 3339. 없으면 받은 시각을 쓴다. |
| `status` | 선택 | `ok`, `error`, `unknown`. 기본은 `unknown`. |
| `level` | 선택 | `debug`, `info`, `warn`, `error`. 기본은 `info`. |
| `session_id`, `tool_use_id` | 선택 | 연결할 세션과 도구 호출. `^[A-Za-z0-9._:-]{1,128}$`. 7장을 본다. |
| `trace_id`, `span_id`, `parent_span_id` | 선택 | 도구가 스스로 쓰는 추적 식별자. 규칙은 위와 같다. |
| `message` | 선택 | 자유로운 글. 가린 뒤 2,048자에서 자른다. |
| `attributes` | 선택 | 32개까지. 키는 `^[A-Za-z0-9._-]{1,64}$`. 값은 문자열, 숫자, 불리언이다. 문자열로 저장하고, 가린 뒤 256자에서 자른다. |
| `duration_ms`, `value` | 선택 | 0 이상의 유한한 수. |
| `unit` | 선택 | `value` 의 단위. 16자까지. |

`kind` 는 이렇게 고른다. 시작과 끝이 있는 일은 `span` 이고 `duration_ms` 를 함께 보낸다. 한 순간에 일어난 일은 `log` 다. 수치 하나는 `metric` 이고 `value` 와 `unit` 을 함께 보낸다.

수신기는 요청 자체도 검사한다. 하나라도 어긋나면 이벤트를 하나도 받지 않는다.

- 메서드는 `POST`, 경로는 `/v1/events` 다. 쿼리 문자열이 없어야 한다.
- `Host` 가 수신 주소와 같아야 한다.
- `Origin` 헤더가 없어야 한다. 웹 페이지가 브라우저로 보내는 요청을 막기 위해서다.
- `Content-Type` 은 `application/json` 이다.
- `Authorization` 헤더는 하나뿐이어야 한다.

## 4. 상한

| 대상 | 상한 |
|---|---|
| 요청 본문 | 256KiB |
| 한 요청의 이벤트 | 100개 |
| `name` | 128자 |
| `message` | 2,048자. 넘는 부분은 자른다. |
| `attributes` | 32개. 값은 256자에서 자른다. |
| `unit` | 16자 |
| 동시에 처리하는 요청 | 4건 |
| 요청 빈도 | 초당 50건. 넘으면 `429` 로 답한다. |

이벤트를 많이 낼 때는 하나씩 보내지 말고 `{"events":[...]}` 로 묶어 보낸다.

## 5. 응답과 거부 이유

요청이 검사를 통과하면 `202` 로 답한다.

```json
{"accepted": 2, "rejected": [{"index": 1, "reason": "..."}]}
```

- `accepted` 는 받은 이벤트의 수다.
- `rejected` 는 받지 않은 이벤트의 목록이다. `index` 는 요청 안에서 그 이벤트가 놓인 순서다.
- 일부가 틀려도 맞는 이벤트는 받는다. 그래서 `202` 만 보고 다 들어갔다고 판단하면 안 된다. `rejected` 가 비었는지 본다.
- `reason` 은 어느 규칙에 걸렸는지 알려 주는 글이다. 사람이 읽으라고 있는 값이다. 코드가 그 문구에 기대면 안 된다.

이벤트가 거부되는 경우는 3장의 표에서 나온다. 필수 필드가 없거나, `version` 이 `1` 이 아니거나, `kind`, `status`, `level` 이 정해진 값이 아니거나, 이름과 식별자가 패턴이나 길이에 맞지 않거나, 수가 음수이거나 유한하지 않거나, 속성이 32개를 넘는 경우다.

거부된 입력은 조용히 사라지지 않는다. 앱이 거부 건수를 세어 확장 이벤트 화면에 보여 준다. 다만 자격이 틀린 요청은 세지 않는다.

`202` 가 아닌 응답은 요청 전체를 받지 않았다는 뜻이다. 3장의 요청 검사에 걸렸거나, 자격이 틀렸거나, 본문이 너무 크거나, 요청이 너무 잦은 경우다.

## 6. 같은 이벤트를 두 번 보내도 되게 하기

전송이 실패했는지 성공했는지 모를 때가 있다. 응답을 받기 전에 제한 시간이 지난 경우가 그렇다. 그때 다시 보내도 이벤트가 두 번 쌓이지 않게 하려면 `id` 를 넣는다.

- `id` 가 있으면 같은 `source` 의 같은 `id` 는 한 번만 저장된다. 다시 보낼 때는 처음과 같은 `id` 를 쓴다.
- `id` 가 없고 `timestamp` 가 있으면 내용이 모두 같은 이벤트를 중복으로 본다. 같은 시각에 같은 내용으로 일어난 서로 다른 일 두 건은 한 건으로 합쳐진다.
- 둘 다 없으면 보낼 때마다 새 이벤트로 저장된다.

`id` 는 이벤트마다 새로 만든 무작위 문자열이면 된다. `trace-emit` 은 무작위 16바이트를 16진수로 적어 넣는다.

## 7. 세션과 도구 호출에 연결하기

이벤트에 `session_id` 와 `tool_use_id` 를 넣으면 앱이 그 세션과 도구 호출에 연결한다.

- `tool_use_id` 가 있는 이벤트는 세션 타임라인에서 그 도구 호출 바로 뒤에 나온다.
- `session_id` 만 있는 이벤트는 시각 순서에 맞는 자리에 나온다.
- 둘 다 없는 이벤트는 "미연계" 다. 확장 이벤트 화면의 출처별 목록에만 나온다.

두 값은 Claude Code 가 훅에 넘기는 표준 입력 JSON 에 들어 있다.

```json
{"hook_event_name": "PostToolUse", "session_id": "...", "tool_use_id": "...", "tool_input": {}}
```

`trace-emit --from-hook` 은 표준 입력에서 이 두 값만 꺼낸다. 도구 인자와 도구 출력은 읽지 않고 버린다. 직접 구현할 때도 두 값만 꺼내 쓰기를 권한다.

훅 입력을 받지 못하는 도구는 두 값을 알 수 없다. MCP 서버가 대개 그렇다. 그때는 비워서 보낸다. 값을 지어내면 다른 세션에 붙는다.

## 8. 가리기

수신기는 저장하기 전에 비밀 값처럼 보이는 부분을 `[가림]` 으로 바꾼다.

- `name` 과 `message` 는 글 전체를 훑어 가린다.
- 속성은 키 이름을 먼저 본다. `password`, `token`, `secret`, `api_key` 같은 낱말이 들어 있으면 값을 통째로 가린다. `token_count` 처럼 개수나 이름을 뜻하는 키는 예외다. 그 밖의 키는 값을 글처럼 훑어 가린다.
- 가리는 것은 알려진 꼴이다. 클라우드와 호스팅 서비스의 키와 토큰, `Bearer` 와 `Basic` 인증 값, URL 에 들어 있는 비밀번호, `--password` 같은 명령줄 인자, `password=...` 같은 대입, 개인 키 블록이다.

가리기는 보조 장치다. 모든 비밀 값을 잡는다고 보장하지 않는다. 이메일 주소, IP 주소, 호스트 이름, 접두사 없는 16진수 문자열은 가리지 않는다. 가장 확실한 방법은 비밀 값을 애초에 이벤트에 넣지 않는 것이다. 명령줄 전체, 환경 변수 목록, 요청과 응답의 본문을 `message` 에 그대로 넣지 않는다.

## 9. 개인정보와 보안

- 수신기는 `127.0.0.1` 에만 묶인다. 다른 기기에서는 닿지 않는다.
- 이벤트는 이 Mac 의 로컬 저장소에만 들어간다. 앱이 밖으로 보내는 곳은 없다.
- 이벤트는 30일이 지나면 지운다. 저장한 수가 200,000건을 넘으면 오래된 것부터 지운다.
- 앱에서 프로젝트를 제외하면 그 프로젝트의 세션에 연결된 이벤트도 함께 지운다. 자료 지우기는 모든 이벤트를 지운다.
- 보내는 쪽도 지킬 것이 있다. 주소가 `http://127.0.0.1:<포트>/v1/events` 꼴이 아니면 보내지 않는다. 리다이렉트를 따라가지 않는다. 프록시를 거치지 않는다. 셋 다 자격이 이 Mac 밖으로 나가지 않게 하려는 것이다.

## 10. `trace-emit`

셸과 스크립트에서 쓰는 전송 명령이다. 주소와 자격은 `TRACE_EVENTS_URL` 과 `TRACE_EVENTS_TOKEN` 에서 읽는다.

```
trace-emit [플래그]                           이벤트 하나를 보낸다
trace-emit run [플래그] -- <명령> [인자...]    명령을 실행하고 span 을 보낸다
```

### 이벤트 하나 보내기

```sh
trace-emit --name build --status ok --duration-ms 812 --attr step=3
```

`--name` 은 꼭 있어야 한다. `--kind` 를 생략하면 `log` 다.

### 명령을 감싸 실행하기

```sh
trace-emit run --name build -- make all
```

- `--` 뒤의 명령을 실행한다. 표준 입력과 출력은 그대로 이어진다.
- 명령이 끝나면 `span` 하나를 보낸다. `timestamp` 는 시작 시각, `duration_ms` 는 걸린 시간이다. `status` 는 종료 코드가 0 이면 `ok`, 아니면 `error` 다. 종료 코드는 속성 `exit_code` 에 들어간다.
- `trace-emit` 은 명령의 종료 코드로 끝난다. 명령을 시작하지 못하면 127 이다. 명령이 시그널로 끝났으면 128 에 시그널 번호를 더한 값이다.
- 받은 시그널(`INT`, `TERM`, `HUP`, `QUIT`, `USR1`, `USR2`)을 명령에 넘긴다.
- 명령은 `trace-emit` 의 환경 변수를 그대로 물려받되 `TRACE_EVENTS_TOKEN` 만 빠진다. 빌드 스크립트나 패키지 설치 훅처럼 명령이 다시 띄우는 프로세스에도 자격이 가지 않는다. `TRACE_EVENTS_URL` 은 남는다. 감싼 명령 안에서 이벤트를 보내야 하면 그 명령이 자격을 따로 받아야 한다.
- `--name` 을 생략하면 명령의 파일 이름이다. `--kind` 를 생략하면 `span` 이다.
- `--status`, `--duration-ms`, `--attr exit_code=...` 는 쓸 수 없다. `run` 이 직접 재는 값이다.

### 플래그

| 플래그 | 뜻 |
|---|---|
| `--name` | 이벤트 이름 |
| `--kind` | `span`, `log`, `metric` |
| `--status` | `ok`, `error`, `unknown` |
| `--level` | `debug`, `info`, `warn`, `error` |
| `--duration-ms` | 걸린 시간(밀리초) |
| `--value`, `--unit` | 수치와 단위 |
| `--message` | 자유로운 글 |
| `--attr key=value` | 속성. 여러 번 쓸 수 있다. 같은 키를 다시 쓰면 뒤의 값이 남는다. |
| `--source` | 출처 이름. 없으면 `TRACE_EVENTS_SOURCE`, 그것도 없으면 `trace-emit` 이다. |
| `--session-id`, `--tool-use-id` | 연결할 세션과 도구 호출 |
| `--trace-id`, `--span-id`, `--parent-span-id` | 추적 식별자 |
| `--from-hook` | 표준 입력의 훅 JSON 에서 `session_id` 와 `tool_use_id` 를 읽는다. 플래그로 준 값이 있으면 그 값을 쓴다. |
| `--verbose` | 이벤트를 보내지 못한 이유를 표준 오류에 적는다. |

값이 빈 플래그는 주지 않은 것으로 본다. `--session-id "$SID"` 에서 변수가 비어 있어도 오류가 아니다.

`--from-hook` 은 표준 입력을 256KiB 까지만 읽는다. 이벤트 하나를 보낼 때는 500ms 를 기다리고, 그 안에 입력이 끝나지 않으면 연결 없이 보낸다.

`trace-emit` 은 보낼 때마다 `id` 와 `timestamp` 를 넣는다. `--message` 는 8,192자, 속성 값은 1,024자에서 먼저 자른다. 수신기가 가린 뒤 다시 자른다.

### 종료 코드와 출력

추적이 꺼져 있다는 이유로 도구가 깨지면 안 된다. 그래서 전송과 관련된 실패는 종료 코드를 바꾸지 않는다.

| 상황 | 이벤트 하나 보내기 | `run` |
|---|---|---|
| 보냈다 | 0 | 명령의 종료 코드 |
| 환경 변수가 없다 | 0. 보내지 않는다. | 명령의 종료 코드 |
| 주소가 `http://127.0.0.1:<포트>/v1/events` 꼴이 아니다 | 0. 보내지 않는다. | 명령의 종료 코드 |
| 수신기가 꺼져 있거나 500ms 안에 답하지 않는다 | 0 | 명령의 종료 코드 |
| 수신기가 이벤트를 거부했다 | 0 | 명령의 종료 코드 |
| 플래그가 잘못됐다 | 2. 보내지 않는다. | 2. 명령을 실행하지 않는다. |

- 플래그가 잘못된 경우만 2 로 끝난다. 모르는 플래그, 값이 없는 플래그, `--name` 누락, `key=value` 꼴이 아닌 `--attr`, 3장의 규칙에 맞지 않는 값이 여기에 든다. 이유는 `--verbose` 없이도 표준 오류에 적는다. 쓰는 사람이 고쳐야 하는 잘못이기 때문이다.
- 환경에서 온 값은 플래그가 아니다. `TRACE_EVENTS_SOURCE` 가 규칙에 맞지 않거나 훅 입력을 읽지 못해도 2 로 끝나지 않는다. 훅 입력을 못 쓰면 연결 없이 보낸다.
- 표준 출력에는 아무것도 쓰지 않는다. 훅의 표준 출력을 해석하는 도구와 부딪히지 않는다. `--help` 만 예외다.
- 오류 문구에 플래그의 값, 주소, 자격을 적지 않는다.

Claude Code 는 훅의 종료 코드 2 를 차단 오류로 다룬다. 훅에 넣기 전에 명령을 한 번 직접 실행해 플래그가 맞는지 확인한다. 실행할 때마다 달라지는 값을 `--name` 이나 식별자 플래그에 넣는다면 명령 끝에 `|| true` 를 붙인다.

## 11. 예제

### curl

```sh
curl --silent --show-error --max-time 1 --noproxy 127.0.0.1 \
  --request POST "$TRACE_EVENTS_URL" \
  --header "Authorization: Bearer $TRACE_EVENTS_TOKEN" \
  --header "Content-Type: application/json" \
  --data '{"version":1,"source":"deploy-script","kind":"log","name":"deploy-started","attributes":{"target":"staging"}}' \
  || true
```

`--max-time` 과 `|| true` 는 수신기가 꺼져 있어도 스크립트가 멈추거나 실패하지 않게 한다.

### Claude Code 훅

Bash 도구가 끝날 때마다 이벤트를 보내는 `PostToolUse` 훅이다. Claude Code 설정 파일의 `hooks` 에 넣는다.

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "trace-emit --from-hook --source my-hooks --name bash-finished"
          }
        ]
      }
    ]
  }
}
```

`--from-hook` 이 훅 입력에서 `session_id` 와 `tool_use_id` 를 읽는다. 그래서 이 이벤트는 세션 타임라인에서 해당 Bash 호출 바로 뒤에 나온다.

### 스크립트 감싸기

```sh
trace-emit run --name nightly-build --attr target=release -- make all
```

`make all` 이 실패하면 이 줄도 같은 종료 코드로 실패한다. 추적을 끈 환경에서도 `make all` 은 그대로 실행된다.

훅이 부르는 스크립트를 감쌀 수도 있다.

```sh
trace-emit run --from-hook --name lint -- ./scripts/lint.sh
```

`trace-emit` 이 훅 입력을 먼저 읽고, 읽은 내용을 스크립트의 표준 입력으로 다시 넘긴다. 스크립트는 감싸기 전과 같은 입력을 받는다. 이때 `trace-emit` 은 표준 입력이 끝날 때까지 최대 0.5초 기다린 뒤에 스크립트를 시작한다.

표준 입력이 터미널이거나 닫히지 않는 파이프라서 0.5초 안에 끝나지 않으면, `trace-emit` 은 더 기다리지 않고 스크립트를 시작한다. 그때까지 읽은 내용과 그 뒤에 들어오는 입력은 그대로 스크립트에 넘어간다. 다만 이 경우 span 은 세션에 연결되지 않는다.

### MCP 서버 (의사 코드)

도구 처리 함수를 감싸 호출마다 `span` 하나를 보낸다.

```
function call_tool(name, arguments):
    started = now()
    status = "ok"
    try:
        return run_tool(name, arguments)
    except error:
        status = "error"
        raise error
    finally:
        send_event({
            "version": 1,
            "id": random_hex(16),
            "source": "my-mcp-server",
            "kind": "span",
            "name": name,
            "timestamp": rfc3339(started),
            "duration_ms": milliseconds(now() - started),
            "status": status
        })

function send_event(event):
    url = env("TRACE_EVENTS_URL")
    token = env("TRACE_EVENTS_TOKEN")
    if url is empty or token is empty:
        return                                  # 추적이 꺼져 있다
    if not matches(url, "^http://127\.0\.0\.1:[0-9]+/v1/events$"):
        return                                  # 이 Mac 밖으로는 보내지 않는다
    try:
        http_post(url,
            headers = {"Authorization": "Bearer " + token,
                       "Content-Type": "application/json"},
            body = json(event),
            timeout = 0.5 seconds,
            follow_redirects = false,
            proxy = none)
    except any error:
        return                                  # 전송 실패가 도구를 깨뜨리면 안 된다
```

- 도구의 인자와 결과를 이벤트에 넣지 않는다. 이름, 걸린 시간, 성공 여부, 횟수 같은 수치만 보낸다.
- 전송은 도구의 응답을 늦추지 않게 한다. 제한 시간을 짧게 두거나 응답을 보낸 뒤에 보낸다.
- `session_id` 와 `tool_use_id` 를 알 수 없으면 비워 둔다. 이벤트는 미연계로 출처 화면에 나온다.

## 12. 알려진 한계

- 도구들이 자격 하나를 같이 쓴다. 출처별 자격은 없다.
- `source`, `session_id`, `tool_use_id` 는 보내는 쪽이 정한다. 한 도구가 다른 도구의 출처 이름을 쓰거나 다른 세션에 이벤트를 붙일 수 있다. 화면의 출처 이름은 보낸 쪽이 주장한 값으로 읽는다.
- 앱이 꺼져 있을 때 다른 프로세스가 같은 포트를 열면 그 프로세스가 자격을 받을 수 있다.
- 자격을 새로 만드는 기능은 아직 없다.
- 가리기는 모든 비밀 값을 잡지 못한다. 8장을 본다.
- 터미널에서 `trace-emit run` 을 쓰다가 Ctrl-C 를 누르면 감싼 명령이 `INT` 를 두 번 받을 수 있다. 터미널이 한 번 보내고 `trace-emit` 이 한 번 더 넘기기 때문이다.
- `trace-emit` 은 전송에 실패한 이벤트를 다시 보내지 않고 쌓아 두지도 않는다. 수신기가 꺼져 있던 동안의 이벤트는 남지 않는다.
