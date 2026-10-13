import axios from 'axios';

const api = axios.create({ baseURL: '/api' });
const cache = new Map();

export const getUsers = () => axios.get('/api/users', { params: { page: 1 } });
export const createUser = (data) => api.post('/users', data);
export const cached = () => cache.get('/api/users');
