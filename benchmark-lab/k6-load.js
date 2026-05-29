import http from 'k6/http';
import { check } from 'k6';

export const options = {
    vus: 2000,
    duration: '60s',
    thresholds: {
        http_req_duration: ['p(99)<200'],
        http_req_failed: ['rate<0.01'],
    },
};

const BASE_URL = __ENV.TARGET === 'nestjs' ? 'http://nestjs:3000' : 'http://appitools:8080';
// Using a dummy JWT here. In our Appitools context it doesn't do RBAC strictly unless we configure it, but we'll send it anyway.
const TOKEN = 'dummy-jwt-token';
const TENANT_ID = 'tenant_3';

export default function () {
    // Generate random page and random status
    const page = Math.floor(Math.random() * 10) + 1;
    const statuses = ['pending', 'published', 'draft', 'archived', 'deleted'];
    const randomStatus = statuses[Math.floor(Math.random() * statuses.length)];
    
    // Create query
    const url = `${BASE_URL}/api/guides?page=${page}&per_page=50&filter[status]=${randomStatus}`;
    
    const res = http.get(url, {
        headers: {
            'Authorization': `Bearer ${TOKEN}`,
            'Host': `${TENANT_ID}.localhost`, // For Appitools router
            'x-user-role': 'admin', // for Appitools explicitly bypassing JWT if needed
            'x-user-id': 'usr_123',
            'x-tenant-id': TENANT_ID
        },
    });

    check(res, {
        'status is 200': (r) => r.status === 200,
    });
}