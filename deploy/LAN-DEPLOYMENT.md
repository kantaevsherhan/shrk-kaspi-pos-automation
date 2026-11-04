# Deployment on 192.168.8.8

Repository: `/home/shrk/shrk-kaspi-pos-automation`.

Start/update the installed deployment with:

```bash
docker compose -f compose.yaml -f compose.lan.yaml up -d --build
```

The application is published only on `127.0.0.1:3001`. An unprivileged nginx gateway uses host networking, listens only on `192.168.8.8:3000`, and accepts only clients from `192.168.8.0/24`. Using host networking preserves the real connection source for the subnet check. Forwarded headers cannot override the access check. Access logging is disabled to keep session/API URLs out of logs. Docker starts both services automatically after reboot.

The host firewall also needs this rule, executed with sudo:

```bash
sudo ufw allow from 192.168.8.0/24 to 192.168.8.8 port 3000 proto tcp comment 'kaspi-pos-lan'
sudo ufw status numbered
```

Keep UFW active with the existing SSH allow rule. Do not add a general port-3000 allow rule or publish the app directly on all host interfaces. The gateway's subnet restriction remains an additional check.

The generated `.env` is mode 0600. Initial administrator credentials are in the ignored `data/initial-access.txt`, mode 0600. Runtime state and the password hash are stored in the named `kaspi_data` Docker volume. Back up that volume together with `.env` so encrypted sessions remain recoverable. No Kaspi account has been connected by the installer.

This deployment uses HTTP inside the requested LAN. `COOKIE_SECURE=false` is required for this address; configure TLS and secure cookies before any future wider deployment.
