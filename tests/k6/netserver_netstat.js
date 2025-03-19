import http from 'k6/http';
import { check, group, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '10s', target: 500 },
    { duration: '10s', target: 1000 },
    { duration: '10s', target: 1500 },
    { duration: '30s', target: 5000 },
    { duration: '10s', target: 1500 },
    { duration: '10s', target: 1000 },
    { duration: '10s', target: 500 },
  ],
};

var host = 0;

export default function () {
  host = (host >= 20000) ? 0 : host +1;
  var ip1 = Math.round(host/100);
  var ip2 = Math.round(host/100);
  //let port = Math.floor(Math.random() * 1000);

  const data = { "data": [
      { 
        "localAddr": { "ip": `192.168.${ip1}.${ip2}`, "name": `host-n${ip1}.${ip2}` }, 
        "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
        "relation": { "mode": "udp", "port": 5256}, 
        "options": {} 
      },{ 
        "localAddr": { "ip": `192.168.${ip1}.${ip2}`, "name": `host-n${ip1}.${ip2}` }, 
        "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
        "relation": { "mode": "udp", "port": 5257}, 
        "options": {} 
      },{ 
        "localAddr": { "ip": `192.168.${ip1}.${ip2}`, "name": `host-n${ip1}.${ip2}` }, 
        "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
        "relation": { "mode": "udp", "port": 5258}, 
        "options": {} 
      },{ 
        "localAddr": { "ip": `192.168.${ip1}.${ip2}`, "name": `host-n${ip1}.${ip2}` }, 
        "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
        "relation": { "mode": "udp", "port": 5259}, 
        "options": {} 
      },{ 
        "localAddr": { "ip": `192.168.${ip1}.${ip2}`, "name": `host-n${ip1}.${ip2}` }, 
        "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
        "relation": { "mode": "udp", "port": 5260}, 
        "options": {} 
      }
    ] 
  }
  //console.log("test write host-", host);
  let res = http.post(`http://127.0.0.1:8084/api/v1/netmap/netstat`, JSON.stringify(data));

  check(res, { 'status was 204': (r) => r.status == 204 });
}