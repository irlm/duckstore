# Every machine

Apply this to every lab machine, in every setup, before the steps in the setup file.

## Network

- Wired Ethernet, all machines on the same switch and subnet. No Wi-Fi.
- A fixed IP (DHCP reservation) and the hostname from the setup file.
- The fastest link the machine has (1 GbE minimum).

## Always on

- Never sleep.
- High-performance power plan. On Linux, set the CPU governor to `performance`.
- Time sync on.
- Install all updates first, then pause automatic updates while tests run.

## Access for Claude

- **User `duck` with SSH key login.** Authorize the laptop's public key `~/.ssh/id_ed25519.pub`.
  - Windows: enable OpenSSH Server. `duck` is an administrator, so the key goes in
    `C:\ProgramData\ssh\administrators_authorized_keys`.
  - macOS: System Settings → General → Sharing → Remote Login.
- **No GitHub credentials** on lab machines. Claude copies the repo with `rsync` over SSH.
- **No passwords in the repo or in chat.** Put them in `lab/lab.env` on the laptop (git-ignored).

## Firewall

- Open only the ports in the setup file, plus 22 (SSH) and 5201 (iperf3).
- Allow them only from the lab subnet. Nothing is exposed to the internet.

## Linux server base

Every Linux machine in the lab gets this:

- **OS:** Ubuntu Server 24.04 LTS, minimal, no desktop.
- **Data disk:** the fastest NVMe or SSD, formatted **XFS** and mounted at `/data`. XFS supports reflinks, so the ETL
  snapshot copy takes milliseconds, like btrfs on the laptop.
- **Docker:**
  - Docker Engine and the compose plugin, from Docker's apt repository (not snap).
  - Set `"data-root": "/data/docker"` in `/etc/docker/daemon.json`.
- **User `duck`:** passwordless sudo (OK only in this private lab), and member of the `docker` group.
- **Packages:** `git rsync curl jq htop sysstat fio iperf3`.
- **No database packages on the host.** Claude runs Postgres, SQL Server and object storage as containers, with the
  same images and settings as the laptop.

## Checks when a setup is done

1. Run `iperf3 -s` on every machine.
2. From `lab-client`, for each other machine:
   - `ping -c 20 <ip>`: the average should be under 0.5 ms.
   - `iperf3 -c <ip> -t 10`: the result should be close to the link speed.
3. From the laptop: `ssh duck@<ip> hostname` works for every machine.
4. Run the extra checks in the setup file.

## Send back

One row per role, including roles that share a machine (same IP):

| Role | Hostname | IP | CPU model | Cores / threads | RAM | Data disk (model, size) | Link speed | OS version | ping avg / iperf3 |
|---|---|---|---|---|---|---|---|---|---|
| | | | | | | | | | |
