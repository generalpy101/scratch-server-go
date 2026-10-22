// k6 stress test for pygoserver: find the breaking point.
//
// Unlike todos.js (realistic users), this uses the "open model"
// (ramping-arrival-rate): k6 starts N sessions per second no matter how
// slow the server gets. That matters because of "coordinated omission":
// with a fixed pool of looping users, a dying server slows responses,
// users take longer per session, and the applied load DROPS just when
// you want maximum pressure. Arrival-rate keeps the pressure constant.
//
// Each session performs ONE weighted-random action, mimicking a real
// traffic mix (mostly reads, some writes) instead of hammering one route.
//
// "Broken" is defined by the thresholds below: more than 5% failed
// requests or p99 latency above 2s for 30s straight aborts the run.
//
// Run:
//
//	k6 run benchmark/k6/stress.js
//
// Watch localhost:6060/dashboard while it runs: the resource that spikes
// or plateaus at the moment latency explodes is your bottleneck.
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
    scenarios: {
        breaking_point: {
            executor: 'ramping-arrival-rate',
            startRate: 100,
            timeUnit: '1s', // rates below are sessions per second
            preAllocatedVUs: 100,
            maxVUs: 3000, // k6 bails out if it needs more than this
            stages: [
                // step load up in plateaus: hold each level so the
                // server reaches steady state before the next step
                { duration: '1m', target: 100 },
                { duration: '1m', target: 250 },
                { duration: '1m', target: 500 },
                { duration: '1m', target: 1000 },
                { duration: '1m', target: 2000 },
                { duration: '1m', target: 4000 },
            ],
        },
    },
    thresholds: {
        // Definition of "broken" for this app. abortOnFail stops the
        // test instead of pointlessly hammering a dead server.
        // delayAbortEval gives the server 30s of grace before judging.
        http_req_failed: [{ threshold: 'rate<0.05', abortOnFail: true, delayAbortEval: '30s' }],
        http_req_duration: [{ threshold: 'p(99)<2000', abortOnFail: true, delayAbortEval: '30s' }],
    },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

function randomString(length) {
    const chars = 'abcdefghijklmnopqrstuvwxyz';
    let out = '';
    for (let i = 0; i < length; i++) {
        out += chars[Math.floor(Math.random() * chars.length)];
    }
    return out;
}

export default function () {
    const roll = Math.random();

    if (roll < 0.40) {
        // 40%: page view — HTML + assets in parallel, like a browser
        http.batch([
            ['GET', BASE_URL + '/todos', null, { tags: { name: 'GET /todos' } }],
            ['GET', BASE_URL + '/assets/css/styles.css', null, { tags: { name: 'GET assets' } }],
            ['GET', BASE_URL + '/assets/js/script.js', null, { tags: { name: 'GET assets' } }],
        ]);
    } else if (roll < 0.70) {
        // 30%: read the todo list via the API
        const res = http.get(BASE_URL + '/api/todos?limit=20', { tags: { name: 'GET /api/todos' } });
        check(res, { 'list 200': (r) => r.status === 200 });
    } else if (roll < 0.90) {
        // 20%: create a todo
        const res = http.post(
            BASE_URL + '/api/todos',
            JSON.stringify({ title: 'stress: ' + randomString(12), completed: false }),
            { headers: { 'Content-Type': 'application/json' }, tags: { name: 'POST /api/todos' } },
        );
        check(res, { 'create 200': (r) => r.status === 200 });
    } else {
        // 10%: delete a random todo (list first, pick a victim)
        const list = http.get(BASE_URL + '/api/todos?limit=50', { tags: { name: 'GET /api/todos' } });
        const items = list.json('items');
        if (items && items.length > 0) {
            const victim = items[Math.floor(Math.random() * items.length)];
            const res = http.del(BASE_URL + '/api/todos?id=' + victim.id, null, { tags: { name: 'DELETE /api/todos' } });
            check(res, { 'delete 2xx/4xx/5xx': (r) => r.status !== 0 });
        }
    }

    // Small pacing so one session isn't a burst of back-to-back calls.
    sleep(0.2);
}
