import { Controller, Get, Query, UseGuards, Req } from '@nestjs/common';
import { PrismaService } from '../prisma.service';
import { FakeJwtGuard } from '../auth/fake-jwt.guard';

@Controller('api/guides')
@UseGuards(FakeJwtGuard)
export class GuidesController {
  constructor(private prisma: PrismaService) {}

  @Get()
  async listGuides(
    @Query('page') page: string = '1',
    @Query('per_page') perPage: string = '50',
    @Query('filter') filter: any,
    @Req() req: any
  ) {
    const tenantId = req.user.tenantId; // Obtenido del FakeJwtGuard
    const status = filter?.status;

    const skip = (Number(page) - 1) * Number(perPage);
    const take = Number(perPage);

    const guides = await this.prisma.guides.findMany({
      where: {
        tenantId: tenantId,
        status: status,
      },
      skip,
      take,
      orderBy: { id: 'asc' }
    });

    return { data: guides };
  }
}