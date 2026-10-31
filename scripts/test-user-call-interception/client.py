"""Real loopback HTTP/session/TOTP client for the synthetic built-app acceptance."""
import base64, hashlib, hmac, http.cookiejar, json, os, struct, subprocess, time
from pathlib import Path
from urllib.request import Request, build_opener, ProxyHandler, HTTPCookieProcessor, HTTPRedirectHandler
from urllib.error import HTTPError
from urllib.parse import urlparse

class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

class SessionContextChanged(RuntimeError):
    pass

class ApiError(RuntimeError):
    def __init__(self, method, path, status, body):
        self.status, self.body, self.path = status, body, path
        safe = {k: body.get(k) for k in ('success','code','message') if k in body} if isinstance(body,dict) else 'non-json response'
        super().__init__(f'{method} {path}: HTTP {status} {safe}')

def totp(secret, counter=None):
    counter = int(time.time())//30 if counter is None else counter
    key=base64.b32decode(secret + '='*((8-len(secret)%8)%8))
    digest=hmac.new(key,struct.pack('>Q',counter),hashlib.sha1).digest(); off=digest[-1]&15
    return f'{(struct.unpack(">I",digest[off:off+4])[0]&0x7fffffff)%1000000:06d}'

class ApiClient:
    def __init__(self, base_url, actor, emit):
        assert urlparse(base_url).hostname in ('127.0.0.1','localhost','::1')
        self.base_url=base_url.rstrip('/');self.account=actor;self.user_id=actor['user_id'];self.token='';self.emit=emit;self.last_counter=-1;self.session_id='';self.access_expires_at=0
        self.opener=build_opener(ProxyHandler({}),HTTPCookieProcessor(http.cookiejar.CookieJar()),NoRedirect())
    def request(self, method, path, payload=None, expected=200, headers=None, _allow_refresh=True):
        assert path.startswith('/api/') and '://' not in path
        # Normal browser-style proactive refresh: never retry a financial POST
        # after an error or an unknown outcome. Refresh precedes all new I/O.
        if (_allow_refresh and self.token and self.access_expires_at<=int(time.time())+90
                and path not in ('/api/user/auth/refresh','/api/user/login','/api/user/login/2fa')):
            self.refresh()
        body=None if payload is None else json.dumps(payload,separators=(',',':')).encode()
        head={'Content-Type':'application/json','Origin':self.base_url,'User-Agent':'SyntheticNewAPICallInterceptionAcceptance/1','New-Api-User':str(self.user_id)}
        if self.token:head['Authorization']='Bearer '+self.token
        if headers:head.update(headers)
        req=Request(self.base_url+path,data=body,headers=head,method=method)
        try:
            with self.opener.open(req,timeout=30) as r: status=r.status; raw=r.read(); self.last_headers=dict(r.headers)
        except HTTPError as e:status=e.code;raw=e.read();self.last_headers=dict(e.headers)
        try:data=json.loads(raw)
        except ValueError:data={'non_json':True}
        allowed=(expected,) if isinstance(expected,int) else tuple(expected)
        if status not in allowed:raise ApiError(method,path,status,data)
        if status==200 and isinstance(data,dict) and data.get('success') is False:raise ApiError(method,path,status,data)
        if status!=200:return data
        return data.get('data',data) if isinstance(data,dict) else data
    def get(self,path):return self.request('GET',path)
    def post(self,path,payload):return self.request('POST',path,payload)
    def put(self,path,payload):return self.request('PUT',path,payload)
    def login(self):
        result=self.post('/api/user/login',{'username':self.account['username'],'password':self.account['password']})
        assert result['require_verification'] is True,'real enabled TOTP must be required at login'
        result=self.post('/api/user/login/2fa',{'flow_token':result['flow_token'],'method':'2fa','code':totp(self.account['totp_secret'])})
        self.token=result['access_token'];assert result['user']['id']==self.user_id
        self.session_id=result['session']['sid'];self.access_expires_at=result['access_expires_at']
        self.emit('real_password_totp_login',{'actor':self.account['username'],'user_id':self.user_id,'token_logged':False})
    def refresh(self):
        # Decode only to compare context identity; server validates the actual
        # cookie/JWT. No claim, token or session identifier is emitted.
        def binding(token):
            payload=token.split('.')[1]
            claims=json.loads(base64.urlsafe_b64decode(payload+'='*((-len(payload))%4)))
            return tuple(claims.get(k) for k in ('sub','sid','uv','sv'))
        old=binding(self.token);old_sid=self.session_id
        result=self.request('POST','/api/user/auth/refresh',None,
            headers={'X-Auth-Session':old_sid},_allow_refresh=False)
        assert result['user']['id']==self.user_id and result['session']['sid']==old_sid
        self.token=result['access_token'];self.access_expires_at=result['access_expires_at']
        unchanged=binding(self.token)==old
        self.emit('real_cookie_session_refresh',{'actor_id':self.user_id,
            'same_session':True,'proof_context_identity_unchanged':unchanged,'credentials_logged':False})
        if not unchanged:
            raise SessionContextChanged('Session authorization version changed; prepare and MFA must restart')
