from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
import json
calls=[]
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  if self.path=='/counter': self.answer({'count':len(calls),'methods':[x[0] for x in calls]}); return
  calls.append(('GET',self.path));self.answer({'mock':'synthetic'})
 def do_POST(self):
  size=int(self.headers.get('Content-Length','0')); self.rfile.read(size)
  calls.append(('POST',self.path));self.answer({'id':'synthetic-normal','object':'chat.completion','created':1,'model':'gpt-4o-mini','choices':[{'index':0,'message':{'role':'assistant','content':'normal upstream fixture'},'finish_reason':'stop'}],'usage':{'prompt_tokens':1,'completion_tokens':1,'total_tokens':2}})
 def answer(self,data):
  raw=json.dumps(data).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
 def log_message(self,*args):pass
ThreadingHTTPServer(('127.0.0.1',35886),Handler).serve_forever()
