#!/usr/bin/env python3
"""Tiny DNS sink for the QA lab: logs every query name and answers REFUSED.

Lab namespaces use it as their only resolver, so the log lists every hostname
the lab panel/agents tried to reach (proof that nothing left the host).
Usage: dnslog.py BIND_IP LOGFILE
"""
import socket
import sys
import time


def qname(pkt):
    i, labels = 12, []
    while i < len(pkt) and pkt[i]:
        n = pkt[i]
        labels.append(pkt[i + 1:i + 1 + n].decode(errors="replace"))
        i += 1 + n
    qtype = int.from_bytes(pkt[i + 1:i + 3], "big") if i + 3 <= len(pkt) else 0
    return ".".join(labels), qtype


def main():
    ip, logfile = sys.argv[1], sys.argv[2]
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind((ip, 53))
    with open(logfile, "a", buffering=1) as f:
        while True:
            pkt, addr = s.recvfrom(2048)
            if len(pkt) < 12:
                continue
            name, qt = qname(pkt)
            f.write(f"{time.strftime('%FT%TZ', time.gmtime())} {addr[0]} {name} type={qt}\n")
            # REFUSED (rcode 5), QR=1, echo id + question
            flags = bytes([0x81 | (pkt[2] & 0x01), 0x85])
            s.sendto(pkt[:2] + flags + pkt[4:6] + b"\x00\x00\x00\x00\x00\x00" + pkt[12:], addr)


if __name__ == "__main__":
    main()
