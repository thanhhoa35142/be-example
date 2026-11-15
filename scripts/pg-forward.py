#!/usr/bin/env python3
"""TCP forwarder: WSL eth0:5435 -> 127.0.0.1:4343 (Postgres in docker).

Exists so Windows `netsh portproxy` (Tailscale-facing) can reach the
docker-published Postgres, which only binds WSL localhost.
Run: nohup python3 scripts/pg-forward.py >/tmp/pg-forward.log 2>&1 &
"""
import asyncio
import logging

LISTEN = ("0.0.0.0", 5435)
TARGET = ("127.0.0.1", 4343)

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")
log = logging.getLogger("pg-forward")


async def pipe(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    try:
        while data := await reader.read(65536):
            writer.write(data)
            await writer.drain()
    except (ConnectionError, asyncio.IncompleteReadError):
        pass
    finally:
        try:
            writer.close()
        except Exception:
            pass


async def handle(client_r, client_w):
    peer = client_w.get_extra_info("peername")
    try:
        up_r, up_w = await asyncio.open_connection(*TARGET)
    except OSError as e:
        log.warning("upstream connect failed for %s: %s", peer, e)
        client_w.close()
        return
    log.info("forward %s", peer)
    await asyncio.gather(pipe(client_r, up_w), pipe(up_r, client_w))


async def main():
    server = await asyncio.start_server(handle, *LISTEN)
    log.info("listening on %s -> %s", LISTEN, TARGET)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
