# Qflow Sync

## Usage
### Create `docker-compose.yml` for this service
```yaml
services:
    qflowsync:
      image: ghcr.io/wintbiit/qflowsync:latest
      environment:
        DRY_RUN: "true"
      volumes: 
        - ./data:/app
```
We temporarily set `DRY_RUN` to `true` to prevent any changes to the files. Once you are sure that everything is working as expected and get table structure data.

### Create `config.json` under `./data`
```json
{
  "qflow": {
    "app_id": "**", // Qflow App ID
    "view_id": "**" // Qflow View ID
  },
  "lark": {
    "app_id": "**", // Lark App ID
    "app_secret": "***", // Lark App Secret
    "app_token": "**", // Lark Bitable App Token
    "table_id": "**" // Lark Bitable Table ID
  },
  "interval": "30m" // Sync interval
}
```

### Create `cookies.txt` under `./data`
```txt
# get cookie somehow, in single line
```

### Run the service
```bash
docker-compose up
```

### Check output structure
The sync uses QingFlow field names and creates any missing Bitable columns. New columns use text, date, or link types based on the sync payload. Existing columns keep their types. The Feishu application needs permission to list fields and to create missing fields.

### Set `DRY_RUN` to `false` in `docker-compose.yml`
```yaml
services:
    qflowsync:
      image: ghcr.io/wintbiit/qflowsync:latest
      restart: always
      environment:
        DRY_RUN: "false"
      volumes: 
        - ./data:/app
```

### Restart the service
```bash
docker-compose up -d
```