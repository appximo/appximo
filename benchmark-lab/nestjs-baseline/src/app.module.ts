import { Module } from '@nestjs/common';
import { GuidesController } from './guides/guides.controller';
import { PrismaService } from './prisma.service';

@Module({
  imports: [],
  controllers: [GuidesController],
  providers: [PrismaService],
})
export class AppModule {}