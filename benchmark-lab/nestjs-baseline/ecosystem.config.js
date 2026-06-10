// pm2 cluster config for the fair-comparison benchmark (S46).
// instances = nproc of the SUT (2 vCPU droplet) so Node uses both cores,
// matching the Go engine's GOMAXPROCS=2.
// DATABASE_URL and JWT_SECRET are inherited from the shell environment at
// `pm2 start` time — never hardcode secrets here.
module.exports = {
  apps: [
    {
      name: 'nestjs-bench',
      script: 'dist/main.js',
      instances: 2,
      exec_mode: 'cluster',
      env: {
        NODE_ENV: 'production',
      },
    },
  ],
};
