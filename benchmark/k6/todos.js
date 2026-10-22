// k6 load test for pygoserver: simulates real todo-app users instead of
// hammering one endpoint in a loop.
//
// A "virtual user" (VU) iteration models one visit:
//
//	1. open /todos + static assets in parallel (like a browser does)
//	2. read the list for a few seconds ("think time")
//	3. 50% chance: add a todo
//	4. 30% chance: list todos and delete a random one
//	5. leave
//
// Think time is the important part: real users spend most of their time
// reading, not clicking. 100 VUs with think time produce far fewer (and
// much more realistic) requests per second than 100 workers in a tight
// loop.
//
// Run:
//
//	k6 run benchmark/k6/todos.js
//	k6 run -e BASE_URL=http://localhost:8080 benchmark/k6/todos.js
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
    scenarios: {
        todo_users: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                // warm up: 25 users arrive over 30s
                { duration: '30s', target: 25 },
                // steady traffic
                { duration: '1m', target: 25 },
                // spike: 100 users arrive over 30s
                { duration: '30s', target: 100 },
                // hold the spike
                { duration: '1m', target: 100 },
                // everyone leaves
                { duration: '20s', target: 0 },
            ],
        },
    },
    // The test FAILS if these aren't met. This is how you turn your
    // DESIGN.md performance targets into an automated gate later.
    thresholds: {
        http_req_failed: ['rate<0.01'],   // < 1% errors
        http_req_duration: ['p(95)<500'], // p95 under 500ms
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

function randomInt(min, max) {
    return min + Math.floor(Math.random() * (max - min + 1));
}

export default function () {
    // 1. Open the todo page. http.batch fires the requests in parallel,
    //    like a browser fetching CSS/JS alongside the HTML.
    const [page] = http.batch([
        ['GET', BASE_URL + '/todos', null, { tags: { name: 'GET /todos' } }],
        ['GET', BASE_URL + '/assets/css/styles.css', null, { tags: { name: 'GET assets' } }],
        ['GET', BASE_URL + '/assets/js/script.js', null, { tags: { name: 'GET assets' } }],
    ]);
    check(page, { 'page loads': (r) => r.status === 200 });

    // 2. Read the list.
    sleep(randomInt(1, 3));

    // 3. Half the visitors add a todo.
    if (Math.random() < 0.5) {
        const res = http.post(
            BASE_URL + '/api/todos',
            JSON.stringify({ title: 'k6: ' + randomString(12), completed: false }),
            { headers: { 'Content-Type': 'application/json' }, tags: { name: 'POST /api/todos' } },
        );
        check(res, { 'todo created': (r) => r.status === 200 });
        sleep(randomInt(1, 2));
    }

    // 4. Some visitors clean up a random todo (read the list, pick one).
    if (Math.random() < 0.3) {
        const list = http.get(BASE_URL + '/api/todos?limit=50', { tags: { name: 'GET /api/todos' } });
        const items = list.json('items');

        if (items && items.length > 0) {
            const victim = items[randomInt(0, items.length - 1)];
            const res = http.del(BASE_URL + '/api/todos?id=' + victim.id, null, { tags: { name: 'DELETE /api/todos' } });
            check(res, { 'todo deleted': (r) => r.status === 200 });
        }

        sleep(randomInt(1, 2));
    }
}
