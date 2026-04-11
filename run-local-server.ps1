Set-Location 'E:\123pan\Downloads\codex2api'
$env:CODEX_PORT='8080'
$env:DATABASE_DRIVER='sqlite'
$env:DATABASE_PATH='./data/codex2api.db'
$env:CACHE_DRIVER='memory'
$env:TZ='Asia/Shanghai'
go run .
