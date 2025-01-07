import http from 'k6/http';
import { check, group, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '10s', target: 500 },
    { duration: '10s', target: 1000 },
    { duration: '10s', target: 1500 },
    { duration: '30s', target: 5000 },
    //{ duration: '10s', target: 1500 },
    { duration: '10s', target: 1000 },
    { duration: '10s', target: 500 },
  ],
};

var host = 0;

export default function () {
  host = (host >= 10000) ? 0 : host +1;
  var ip1 = Math.round(host/1000);
  var ip2 = Math.round(host/100);
  //var host = Math.floor(Math.random() * 100);
  //let port = Math.floor(Math.random() * 1000);

  group('01. Write status', () => {
    const data = { "data": [
        { 
            "localAddr": { "ip": `192.167.${ip1}.${ip2}`, "name": `host-s${host}` }, 
            "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
            "relation": { "mode": "udp", "port": 5256, "result": 1}, 
            "options": {} 
        },{ 
            "localAddr": { "ip": `192.167.${ip1}.${ip2}`, "name": `host-s${host}` }, 
            "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
            "relation": { "mode": "udp", "port": 5257, "result": 1}, 
            "options": {} 
        },{ 
            "localAddr": { "ip": `192.167.${ip1}.${ip2}`, "name": `host-s${host}` }, 
            "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
            "relation": { "mode": "udp", "port": 5258, "result": 1}, 
            "options": {} 
        },{ 
            "localAddr": { "ip": `192.167.${ip1}.${ip2}`, "name": `host-s${host}` }, 
            "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
            "relation": { "mode": "udp", "port": 5259, "result": 1}, 
            "options": {} 
        },{ 
            "localAddr": { "ip": `192.167.${ip1}.${ip2}`, "name": `host-s${host}` }, 
            "remoteAddr": { "ip": "192.168.0.1", "name": "remotehost" }, 
            "relation": { "mode": "udp", "port": 5260, "result": 1}, 
            "options": {} 
        }
      ] 
    }
    //console.log("test write host-", host);
    let res = http.post(`http://127.0.0.1:8084/api/v1/netmap/records`, JSON.stringify(data));

    check(res, { 'status was 204': (r) => r.status == 204 });

    group('02. Read status', () => {
        //console.log("test read host-", host);
        let res = http.get(`http://127.0.0.1:8084/api/v1/netmap/records?src_name=host-s${host}`);

        check(res, { 'status was 200': (r) => r.status == 200 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[0].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[1].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[2].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[3].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[4].relation.result == 1 });

        sleep(0.3);
    });

    group('03. Read status', () => {
        //console.log("test read host-", host);
        let res = http.get(`http://127.0.0.1:8086/api/v1/netmap/records?src_name=host-s${host}`);

        check(res, { 'status was 200': (r) => r.status == 200 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[0].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[1].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[2].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[3].relation.result == 1 });
        check(res.json(), { 'retrieved alerts list': (r) => r.data[4].relation.result == 1 });

        sleep(0.3);
    });

    sleep(0.3);
  });

}