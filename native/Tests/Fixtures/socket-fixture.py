"""Local RFC6455 fixture; no third-party runtime dependency."""
import base64, hashlib, json, socket, struct, sys
s = socket.socket()
s.bind(('127.0.0.1', 0))
s.listen(1)
s.settimeout(15)
open(sys.argv[1], 'w').write(str(s.getsockname()[1]))
c, _ = s.accept()
c.settimeout(15)
def exact(n):
    b = b''
    while len(b) < n:
        chunk = c.recv(n - len(b))
        if not chunk: raise EOFError()
        b += chunk
    return b
def read():
    h = exact(2)
    n = h[1] & 127
    if n == 126: n = struct.unpack('!H', exact(2))[0]
    if n == 127: n = struct.unpack('!Q', exact(8))[0]
    mask = exact(4) if h[1] & 128 else None
    body = exact(n)
    if mask: body = bytes(v ^ mask[i % 4] for i, v in enumerate(body))
    assert h[0] & 15 == 1
    return json.loads(body)
def send(value):
    data = json.dumps(value).encode()
    h = bytes([129, len(data)]) if len(data) < 126 else bytes([129,126])+struct.pack('!H',len(data))
    c.sendall(h+data)
headers = b''
while not headers.endswith(b'\r\n\r\n'): headers += exact(1)
text = headers.decode()
assert 'Authorization: Bearer fixture-secret'.lower() in text.lower()
assert 'X-Native-Node-Id: fixture-node'.lower() in text.lower()
key = next(line.split(':',1)[1].strip() for line in text.split('\r\n') if line.lower().startswith('sec-websocket-key:'))
accept = base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
c.sendall(('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+accept+'\r\n\r\n').encode())
hello = read()
assert hello['type'] == 'hello' and hello['name'] == 'Fixture Mac'
assert hello['capabilities'] == ['ocr','scan']
send({'type':'welcome'})
send({'type':'ocr','requestId':'invalid-job'})
ack = read()
assert ack['type'] == 'ack' and ack['requestId'] == 'invalid-job' and ack['accepted'] is False
send({'type':'control','action':'drain'})
capacity = read()
assert capacity['type'] == 'capacity' and capacity['capacity']['state'] == 'paused'
send({'type':'control','action':'resume'})
capacity = read()
assert capacity['capacity']['operatorLimit'] == 6
c.close()
s.close()
