# wcy new-api deployment

This directory uses a local production compose file instead of the upstream example.

Common commands:

```bash
cd /home/wcy/new-api
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml logs -f new-api
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml down
```

The service is exposed on port 3000. First browser visit should open the New API initialization page.
Secrets are stored in `.env`, which should not be committed.


Docker Hub pulls are configured to use the local Clash/Mihomo proxy at `http://127.0.0.1:7897` via `/etc/systemd/system/docker.service.d/http-proxy.conf`.
If image pulls fail after a reboot, make sure Clash Verge / Mihomo is running first, then run:

```bash
sudo systemctl restart docker
```
