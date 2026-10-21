<div align="center">
  <h1>⚡ Transloader</h1>
  <p><b>High-Speed Multi-Connection Direct Cloud File Transloader in Go</b></p>
  <p><i>Strictly No-Torrents • Heroku TOS Compliant • Multi-Threaded Range Acceleration • Custom Headers Support • Ephemeral Auto-Purge Cache</i></p>

  <p>
    <a href="https://heroku.com/deploy">
      <img src="https://www.herokucdn.com/deploy/button.svg" alt="Deploy to Heroku">
    </a>
  </p>
</div>

---

## 🚀 Overview

**Transloader** is an ultra-lightweight, high-throughput cloud downloading engine designed specifically for deployment on PaaS platforms like **Heroku**, **Render**, **Railway**, or any Linux VPS.

It fetches large files from remote servers using **parallel multi-connection HTTP byte ranges (4x, 8x, 16x)**, stores them temporarily on ephemeral disk cache, and serves them to you as **pure direct downloads** with full RFC 7233 byte-range support for accelerators like IDM, aria2c, and Chrome.

---

## 🛡️ Why It Is 100% Heroku Compliant

* 🚫 **Strictly NO Torrents or P2P:** Heroku strictly bans BitTorrent protocols and terminates accounts that engage in P2P leeching. Transloader relies **only on standard HTTP/HTTPS application protocols**.
* ⚡ **Ultra-Low Memory Footprint (~1 MB RAM):** Built in pure Go with zero runtime bloat. Perfect for 512MB Eco/Basic dynos.
* 🧹 **Ephemeral Auto-Purge Manager:** Automatically removes expired files after a configurable retention window (default: 6 hours) or when disk usage nears dyno limits.
* 🔄 **Dynamic `$PORT` Binding & Keep-Alive:** Binds directly to Heroku's `$PORT` and provides a `/health` endpoint for free 24/7 keep-alive monitoring (via UptimeRobot).

---

## 🌟 Key Features

1. **⚡ Parallel Multi-Connection Acceleration:**
   - Probes remote servers for `206 Partial Content` and splits transfers across **4, 8, or 16 parallel connections**.
   - Bypasses per-connection server throttling and maximizes server gigabit bandwidth.

2. **🔧 Full Custom Headers Support:**
   - Pass custom `User-Agent`, `Cookie`, `Referer`, `Authorization`, or arbitrary custom headers (`Key: Value`) per download.
   - Headers are forwarded seamlessly to all parallel worker connections.

3. **📥 Pure Direct Downloadable (No In-Browser Viewing):**
   - Files are served with forced `Content-Disposition: attachment` headers.
   - Built with RFC 7233 range support (`Accept-Ranges: bytes`) so multi-connection download managers (IDM, FDM, aria2c, curl) can accelerate downloads from your Transloader.

4. **☁️ Multi-Source Resolution:**
   - **Direct Links:** Any HTTP/HTTPS file URL.
   - **Google Drive:** Direct resolution for large file virus-scan confirmations.
   - **Media Sites:** Optional auto-managed `yt-dlp` integration for video/audio links (YouTube, Twitter/X, TikTok, etc.).

5. **📡 Real-Time SSE Dashboard:**
   - Live download speed (MB/s), downloaded/total bytes, ETA, and progress bar updated every 300ms without page reloads.

---

## 🕹️ Deployment to Heroku

### Option A: 1-Click Deploy
Click the **Deploy to Heroku** button above or use your GitHub repository URL:
```text
https://heroku.com/deploy?template=https://github.com/<your-username>/transloader
```

### Option B: Deploy via Heroku CLI
```bash
# 1. Login to Heroku
heroku login

# 2. Create your Heroku app
heroku create your-transloader-app

# 3. (Optional) Set environment variables
heroku config:set FILE_RETENTION_HOURS=6
heroku config:set MAX_CONCURRENT_DOWNLOADS=3
heroku config:set ADMIN_PASSWORD="your_secure_password"

# 4. Deploy code
git push heroku main

# 5. Open your app
heroku open
```

---

## ⚙️ Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | Port to bind (Heroku sets this automatically). |
| `DOWNLOAD_DIR` | `downloads` | Directory on disk to store cached files. |
| `MAX_CONCURRENT_DOWNLOADS` | `3` | Maximum active simultaneous download jobs. |
| `FILE_RETENTION_HOURS` | `6` | Hours before files are automatically deleted from disk cache. |
| `ADMIN_PASSWORD` | *(none)* | Optional Basic Auth password to restrict web access. |
| `AUTO_INSTALL_YTDLP` | `true` | Automatically downloads static `yt-dlp` binary if needed. |

---

## 📡 REST API Reference

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `GET` | `/health` | Keep-alive check for UptimeRobot (`{"status":"ok"}`). |
| `GET` | `/api/stats` | Memory, disk usage, and active task stats. |
| `GET` | `/api/tasks` | List of running and pending download tasks. |
| `GET` | `/api/files` | List of all completed downloadable files. |
| `POST` | `/api/download` | Start a new transload task (`{"url": "...", "connections": 8}`). |
| `POST` | `/api/cancel/{id}` | Cancel a running download. |
| `DELETE` | `/api/files/{name}` | Delete a specific cached file. |
| `DELETE` | `/api/files` | Delete all cached files immediately. |
| `GET` | `/api/events` | Server-Sent Events (SSE) real-time event stream. |
| `GET` | `/files/{name}` | **Direct Download** with RFC 7233 byte-range support. |
