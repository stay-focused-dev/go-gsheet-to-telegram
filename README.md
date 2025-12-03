# How to run

```shell
go run cmd/drivewatch/main.go -creds $DRIVE_CREDS  -sheet $DRIVE_SHEET -webhook $DRIVE_WEBHOOK -port $DRIVE_WEBHOOK_PORT
go run cmd/sheetcontent/main.go -creds $DRIVE_CREDS -sheet $DRIVE_SHEET
```

## On localhost

```shell
scripts/dev-tunnel.fish
```